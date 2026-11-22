//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	inboundconfiguration "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	"github.com/PapilioVPN/node/internal/testutil"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// The fixtures are native engine configuration inputs. Each advertised account
// type is checked against the actual UserManager readback, not a copied registry.
func TestNativeCompatibilityMatchesEightRealUserManagers(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{17}, 32))
	sskey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{18}, 16))
	private, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{19}, 32))
	if err != nil {
		t.Fatal(err)
	}
	public := base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
	families := []struct {
		tag, protocol, account string
		settings               map[string]any
	}{
		{"vless", "vless", "xray.proxy.vless.Account", map[string]any{"decryption": "none", "clients": []any{map[string]any{"email": "member@example.test", "id": "fa672bef-350c-48e4-bef4-513f2fe4e6ea"}}}},
		{"vmess", "vmess", "xray.proxy.vmess.Account", map[string]any{"clients": []any{map[string]any{"email": "member@example.test", "id": "c63d1fe7-57c8-4711-91d4-641402125147"}}}},
		{"trojan", "trojan", "xray.proxy.trojan.Account", map[string]any{"clients": []any{map[string]any{"email": "member@example.test", "password": "compatibility-secret"}}}},
		{"classic-ss", "shadowsocks", "xray.proxy.shadowsocks.Account", map[string]any{"clients": []any{map[string]any{"email": "member@example.test", "method": "aes-128-gcm", "password": "compatibility-secret"}}}},
		{"ss2022", "shadowsocks", "xray.proxy.shadowsocks_2022.Account", map[string]any{"method": "2022-blake3-aes-128-gcm", "password": sskey, "clients": []any{map[string]any{"email": "member@example.test", "password": sskey}}}},
		{"wireguard", "wireguard", "xray.proxy.wireguard.PeerConfig", map[string]any{"noKernelTun": true, "secretKey": key, "address": []string{"10.71.0.1/24"}, "peers": []any{map[string]any{"email": "member@example.test", "publicKey": public, "allowedIPs": []string{"10.71.0.2/32"}}}}},
		{"hysteria2", "hysteria", "xray.proxy.hysteria.account.Account", map[string]any{"version": 2, "clients": []any{map[string]any{"email": "member@example.test", "auth": "compatibility-secret"}}}},
		{"masque", "masque", "xray.proxy.masque.Account", map[string]any{"address": []string{"10.72.0.1/24"}, "clients": []any{map[string]any{"email": "member@example.test", "pass": "compatibility-secret"}}}},
	}
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			data, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			var configuration map[string]any
			if err := json.Unmarshal(data, &configuration); err != nil {
				t.Fatal(err)
			}
			var inbounds []any
			var reserved []*net.TCPListener
			var packets []net.PacketConn
			for _, family := range families {
				// Reserve the same TCP/UDP port for protocols that can own both sockets.
				listener := reserveTCP(t)
				packet, err := net.ListenPacket("udp", listener.Addr().String())
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { packet.Close() })
				packets = append(packets, packet)
				reserved = append(reserved, listener)
				inbound := map[string]any{"tag": family.tag, "protocol": family.protocol, "listen": "127.0.0.1", "port": listener.Addr().(*net.TCPAddr).Port, "settings": family.settings}
				if family.protocol == "hysteria" || family.protocol == "masque" {
					stream := map[string]any{"network": family.protocol, "security": "tls", "tlsSettings": map[string]any{"alpn": []string{"h3"}, "certificates": []any{map[string]any{"certificateFile": pki.Server.Certificate, "keyFile": pki.Server.PrivateKey}}}}
					if family.protocol == "hysteria" {
						stream["hysteriaSettings"] = map[string]any{"version": 2}
					}
					inbound["streamSettings"] = stream
				}
				inbounds = append(inbounds, inbound)
			}
			configuration["inbounds"] = inbounds
			data, err = json.Marshal(configuration)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.config.Instances[0].Xray.Configuration, data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, p := range packets {
				p.Close()
			}
			for _, r := range reserved {
				r.Close()
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			connection, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			native := handlers.NewHandlerServiceClient(connection)
			ctx, stop := context.WithTimeout(context.Background(), time.Second)
			built, err := native.ListInbounds(ctx, &handlers.ListInboundsRequest{})
			stop()
			if err != nil {
				t.Fatal(err)
			}
			for _, handler := range built.Inbounds {
				raw, err := proto.Marshal(handler)
				if err != nil {
					t.Fatal(err)
				}
				descriptor, _, err := (inboundconfiguration.Inspector{}).Candidate(raw)
				if err != nil || !descriptor.LiveAccounts {
					t.Fatal("native candidate structural preflight", handler.Tag, err)
				}
			}
			for _, family := range families {
				observed := compatibility(t, c, transport, family.tag)
				if !observed.Inbound.LiveAccounts || observed.Inbound.AccountType != family.account || observed.Inbound.Direction != "inbound" || observed.Inbound.Reason != "supported" {
					t.Fatal(family.tag, observed)
				}
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				users, err := native.GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: family.tag, Email: "member@example.test"})
				cancel()
				if err != nil || len(users.Users) != 1 || users.Users[0].Account.Type != observed.Inbound.AccountType {
					t.Fatal("compatibility disagrees with native manager", family.tag, err)
				}
			}
		})
	}
}
