//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/app/services"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	xrayapi "github.com/PapilioVPN/node/internal/modules/engines/xray/infra/api"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"google.golang.org/protobuf/proto"
)

// This verifies changes to real native user managers for all eight families.
// End-to-end protocol authentication/traffic is verified separately from registry state.
func TestLiveUserAdditionUpdatesEveryBaselineNativeUserManager(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	instance, health := configuredXray(t, certificates.Directory, "edge")
	port := func(network string) int {
		if network == "tcp" {
			return userTestPort(t)
		}
		listener, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		return listener.LocalAddr().(*net.UDPAddr).Port
	}
	key := bytes.Repeat([]byte{7}, 16)
	seed := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 16))
	serverKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 16))
	tls := certificates.Config().Communications.GRPC.TLS
	stream := func(network string, alpn string) map[string]any {
		settings := map[string]any{"network": network, "security": "tls", "tlsSettings": map[string]any{
			"alpn": []string{alpn}, "certificates": []map[string]any{{"certificateFile": tls.CertificateFile, "keyFile": tls.PrivateKeyFile, "oneTimeLoading": true}},
		}}
		if network == "hysteria" {
			settings["hysteriaSettings"] = map[string]any{"version": 2}
		}
		return settings
	}
	cases := []struct {
		tag, protocol, network string
		settings               map[string]any
		stream                 map[string]any
		user                   *xrayv1.InboundUser
	}{
		{"vless", "vless", "tcp", map[string]any{"decryption": "none", "clients": []any{}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Vless{Vless: &xrayv1.VLESSAccount{Id: "vless-native-id"}}}},
		{"vmess", "vmess", "tcp", map[string]any{"clients": []any{}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Vmess{Vmess: &xrayv1.VMessAccount{Id: "vmess-native-id"}}}},
		{"trojan", "trojan", "tcp", map[string]any{"clients": []any{}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Trojan{Trojan: &xrayv1.PasswordAccount{Password: "test-only-trojan"}}}},
		{"shadowsocks", "shadowsocks", "tcp", map[string]any{"clients": []any{}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Shadowsocks{Shadowsocks: &xrayv1.ShadowsocksAccount{Password: "test-only-ss", Cipher: xrayv1.ShadowsocksCipher_SHADOWSOCKS_CIPHER_AES_128_GCM, IvCheck: true}}}},
		{"ss2022", "shadowsocks", "tcp", map[string]any{"method": "2022-blake3-aes-128-gcm", "password": serverKey, "clients": []map[string]any{{"email": "seed@local", "password": seed}}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Shadowsocks_2022{Shadowsocks_2022: &xrayv1.Shadowsocks2022Account{Key: key}}}},
		{"hysteria", "hysteria", "udp", map[string]any{"version": 2, "clients": []any{}}, stream("hysteria", "h3"), &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Hysteria{Hysteria: &xrayv1.HysteriaAccount{Auth: "test-only-auth"}}}},
		{"masque", "masque", "tcp", map[string]any{"address": []string{"10.254.1.1/30"}, "clients": []any{}}, stream("masque", "h2"), &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Masque{Masque: &xrayv1.PasswordAccount{Password: "test-only-masque"}}}},
		{"wireguard", "wireguard", "udp", map[string]any{"secretKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)), "address": []string{"10.254.2.1/24"}, "noKernelTun": true, "peers": []map[string]any{{"publicKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32)), "allowedIPs": []string{"10.254.2.3/32"}}}}, nil, &xrayv1.InboundUser{Account: &xrayv1.InboundUser_Wireguard{Wireguard: &xrayv1.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), PreSharedKey: bytes.Repeat([]byte{8}, 32), AllowedIps: []string{"10.254.2.2/32"}, KeepAliveSeconds: new(uint32)}}}},
	}
	content, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	config["inbounds"].([]any)[0].(map[string]any)["tag"] = "proxy"
	type boundListener struct{ network, address string }
	var listeners []boundListener
	for _, tc := range cases {
		listenPort := port(tc.network)
		inbound := map[string]any{"tag": tc.tag, "listen": "127.0.0.1", "port": listenPort, "protocol": tc.protocol, "settings": tc.settings}
		if tc.stream != nil {
			inbound["streamSettings"] = tc.stream
		}
		config["inbounds"] = append(config["inbounds"].([]any), inbound)
		listeners = append(listeners, boundListener{tc.network, net.JoinHostPort("127.0.0.1", fmt.Sprint(listenPort))})
	}
	content, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(instance.Xray.ConfigFile, content, 0o600); err != nil {
		t.Fatal(err)
	}
	configureXrayAPI(t, &instance, "unix", []string{"HandlerService"})
	instance.Xray.API.MaxReceiveBytes = 64 * 1024
	original, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{instance}
	cfg.Communications.GRPC.Permissions.ManageEngineUsers = true
	cfg.Communications.GRPC.Permissions.ReadEngineUsers = true
	var server *rpc.Server
	var cleanup struct {
		fx.In
		Closers []io.Closer `group:"module_resources"`
	}
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &cleanup))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		_ = server.Close()
		for _, closer := range cleanup.Closers {
			if err := closer.Close(); err != nil {
				t.Error(err)
			}
		}
		assertInstancePortsReleased(t, health)
		for _, binding := range listeners {
			if binding.network == "tcp" {
				assertInstancePortsReleased(t, binding.address)
			} else {
				listener, err := net.ListenPacket("udp", binding.address)
				if err != nil {
					t.Error("native UDP listener leaked")
				} else {
					listener.Close()
				}
			}
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := xrayTraffic(ctx, health); err != nil {
		t.Fatalf("all-family native configuration failed to become ready: %v", err)
	}
	connection, err := grpc.NewClient("unix://"+instance.Xray.API.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	native := handlers.NewHandlerServiceClient(connection)
	observer, err := xrayapi.NewClient(xrayapi.Config{Network: instance.Xray.API.Network, Address: instance.Xray.API.Address, RequestTimeout: instance.Xray.API.RequestTimeout, MaxReceiveBytes: instance.Xray.API.MaxReceiveBytes, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	// Private application reads remain independent of the pending public API decision.
	observations := services.NewUserObservationService(map[instances.InstanceID]services.UserObservationReader{instance.ID: observer})
	client := xrayClient(t, server, certificates)
	for _, tc := range cases {
		t.Run(tc.tag, func(t *testing.T) {
			before, err := client.GetInboundUserCount(ctx, &xrayv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: tc.tag})
			if err != nil {
				t.Fatal(err)
			}
			if before.InstanceId != "edge" || before.InboundTag != tc.tag || before.ObservedAtUnixMs <= 0 {
				t.Fatal("count response lost native selection/observation time")
			}
			selection := domain.InboundUserSelection{InboundTag: tc.tag, Email: "added@local"}
			absent, err := observations.GetInboundUser(ctx, instance.ID, selection)
			if err != nil || absent.Observation.User != nil || absent.Observation.ObservedAt.IsZero() {
				t.Fatal("real missing native user was not a completed absent observation")
			}
			tc.user.Email, tc.user.Level = "added@local", 23
			if _, err := client.AddInboundUser(ctx, &xrayv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: tc.tag, User: tc.user}); err != nil {
				t.Fatalf("real native addition rejected: %v", err)
			}
			after, err := client.GetInboundUserCount(ctx, &xrayv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: tc.tag})
			if err != nil || after.Count != before.Count+1 {
				t.Fatalf("acknowledgement did not change real native membership: %v", err)
			}
			users, err := native.GetInboundUsers(ctx, &handlers.GetInboundUserRequest{Tag: tc.tag, Email: "added@local"})
			if err != nil || len(users.GetUsers()) != 1 || users.Users[0].GetEmail() != "added@local" || users.Users[0].GetLevel() != 23 || users.Users[0].GetAccount() == nil {
				t.Fatal("native stored identity/level/account differs; credentials withheld")
			}
			one, err := observations.GetInboundUser(ctx, instance.ID, selection)
			if err != nil || one.Observation.User == nil || one.Observation.User.Email != "added@local" || one.Observation.User.Level != 23 || one.Observation.User.Account.TypeName != users.Users[0].Account.Type || !bytes.Equal(one.Observation.User.Account.Protobuf, users.Users[0].Account.Value) {
				t.Fatal("real account observation changed native identity/level/payload; credentials withheld")
			}
			expected := before.Count + 1
			if tc.tag == "wireguard" {
				// Native WireGuard permits multiple keys with one email. Listing must
				// retain both records; a single lookup makes no uniqueness assertion.
				duplicate := proto.Clone(tc.user).(*xrayv1.InboundUser)
				duplicate.GetWireguard().PublicKey = bytes.Repeat([]byte{5}, 32)
				duplicate.GetWireguard().AllowedIps = []string{"10.254.2.4/32"}
				if _, err := client.AddInboundUser(ctx, &xrayv1.AddInboundUserRequest{InstanceId: "edge", InboundTag: tc.tag, User: duplicate}); err != nil {
					t.Fatal(err)
				}
				expected++
			}
			list, err := observations.ListInboundUsers(ctx, instance.ID, tc.tag)
			if err != nil || int64(len(list.Observation.Users)) != expected || list.Observation.ObservedAt.IsZero() {
				t.Fatal("real native user collection was truncated or invalid")
			}
			selected, anonymous := 0, 0
			for _, user := range list.Observation.Users {
				if user.Email == "added@local" {
					selected++
				}
				if user.Email == "" {
					anonymous++
				}
			}
			if tc.tag == "wireguard" {
				if selected != 2 || anonymous != 1 {
					t.Fatal("native duplicates or anonymous peer were dropped")
				}
				if _, err := client.RemoveInboundUser(ctx, &xrayv1.RemoveInboundUserRequest{InstanceId: "edge", InboundTag: tc.tag, Email: "added@local"}); err != nil {
					t.Fatal(err)
				}
			} else if selected != 1 {
				t.Fatal("native selected user was lost")
			}
			if _, err := client.RemoveInboundUser(ctx, &xrayv1.RemoveInboundUserRequest{InstanceId: "edge", InboundTag: tc.tag, Email: "added@local"}); err != nil {
				t.Fatal(err)
			}
			after, err = client.GetInboundUserCount(ctx, &xrayv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: tc.tag})
			if err != nil || after.Count != before.Count {
				t.Fatalf("native removal did not restore original membership: %v", err)
			}
			absent, err = observations.GetInboundUser(ctx, instance.ID, selection)
			if err != nil || absent.Observation.User != nil {
				t.Fatal("removed native user still appeared in observations")
			}
		})
	}
	current, err := os.ReadFile(instance.Xray.ConfigFile)
	if err != nil || !bytes.Equal(original, current) {
		t.Fatal("live user mutations changed the persisted configuration")
	}
	for _, tag := range []string{"missing", "proxy"} {
		response, err := client.GetInboundUserCount(ctx, &xrayv1.GetInboundUserCountRequest{InstanceId: "edge", InboundTag: tag})
		if response != nil || status.Code(err) != codes.Unavailable {
			t.Fatalf("generic native missing/non-user-manager error became a successful count: %v", err)
		}
	}
}
