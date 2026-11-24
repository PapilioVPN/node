package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestSyncVLESSFlow(t *testing.T) {
	f := startFixture(t)
	const id = "3a650510-92a3-4c91-8001-1a1010300001"
	for _, step := range []struct {
		body           string
		added, removed uint32
	}{
		{`{"email":"vision@example.test","uuid":"` + id + `","flow":"xtls-rprx-vision"}`, 1, 0},
		{`{"email":"vision@example.test","uuid":"` + id + `","flow":"xtls-rprx-vision"}`, 0, 0},
		{`{"email":"vision@example.test","uuid":"` + id + `","flow":""}`, 1, 1},
	} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.http.URL+accountsPath+"/membership", bytes.NewBufferString(step.body))
		require(t, err)
		resp, err := f.http.Client().Do(req)
		require(t, err)
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			t.Fatalf("SyncUser status = %d", resp.StatusCode)
		}
		var result membership.SyncResult
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		require(t, err)
		if result.Outcome != membership.Confirmed || result.Added != step.added || result.Removed != step.removed {
			t.Fatalf("SyncUser = %+v", result)
		}
		users := f.nativeUsers(t, "vless")
		if len(users) != 1 {
			t.Fatalf("VLESS membership after SyncUser = %d", len(users))
		}
		var account native.VlessAccount
		require(t, proto.Unmarshal(users[0].Account.Value, &account))
		wantFlow := "xtls-rprx-vision"
		if step.removed == 1 {
			wantFlow = ""
		}
		if account.Id != id || account.Flow != wantFlow {
			t.Fatal("VLESS live account has unexpected ID or flow")
		}
	}
}

func TestSyncNativeAccountProtocols(t *testing.T) {
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	key := func(value byte) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 16))
	}
	wgKey := func(value byte) string {
		private, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{value}, 32))
		require(t, err)
		return hex.EncodeToString(private.PublicKey().Bytes())
	}
	key256 := func(value byte) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
	}
	security := testTLS(t)
	for _, tc := range []struct {
		name, protocol, settings, stream string
		first, next                      *accountsv1.Credential
		wantBefore                       int
		wantValue                        string
	}{
		{"vmess", "vmess", `{"clients":[]}`, "",
			&accountsv1.Credential{Protocol: "vmess", Uuid: "ece716cf-6ca5-4419-bddb-0671c22a2633"},
			&accountsv1.Credential{Protocol: "vmess", Uuid: "96493d9c-85d0-4339-a357-b80e17b71193"}, 0, "96493d9c-85d0-4339-a357-b80e17b71193"},
		{"trojan", "trojan", `{"clients":[]}`, "",
			&accountsv1.Credential{Protocol: "trojan", Password: "initial-pass"},
			&accountsv1.Credential{Protocol: "trojan", Password: "updated-pass"}, 0, "updated-pass"},
		{"shadowsocks", "shadowsocks", `{"clients":[]}`, "",
			&accountsv1.Credential{Protocol: "shadowsocks", Method: "aes-128-gcm", Password: "initial-pass"},
			&accountsv1.Credential{Protocol: "shadowsocks", Method: "aes-256-gcm", Password: "updated-pass"}, 0, "updated-pass"},
		{"shadowsocks2022", "shadowsocks", fmt.Sprintf(`{"method":"2022-blake3-aes-128-gcm","password":%q,"clients":[{"email":"seed","password":%q}]}`, key(1), key(2)), "",
			&accountsv1.Credential{Protocol: "shadowsocks2022", Key: key(3)},
			&accountsv1.Credential{Protocol: "shadowsocks2022", Key: key(4)}, 1, key(4)},
		{"shadowsocks2022-aes256", "shadowsocks", fmt.Sprintf(`{"method":"2022-blake3-aes-256-gcm","password":%q,"clients":[{"email":"seed","password":%q}]}`, key256(1), key256(2)), "",
			&accountsv1.Credential{Protocol: "shadowsocks2022", Key: key256(3)},
			&accountsv1.Credential{Protocol: "shadowsocks2022", Key: key256(4)}, 1, key256(4)},
		{"hysteria", "hysteria", `{"version":2,"clients":[]}`, `"network":"hysteria",` + security,
			&accountsv1.Credential{Protocol: "hysteria", Auth: "initial-auth"},
			&accountsv1.Credential{Protocol: "hysteria", Auth: "updated-auth"}, 0, "updated-auth"},
		{"masque", "masque", `{"address":["198.18.17.1/24"],"clients":[]}`, `"network":"masque","masqueSettings":{"path":"/.well-known/masque/ip/*/*/"},` + security,
			&accountsv1.Credential{Protocol: "masque", Password: "initial-pass"},
			&accountsv1.Credential{Protocol: "masque", Password: "updated:pass"}, 0, "updated:pass"},
		{"wireguard", "wireguard", fmt.Sprintf(`{"secretKey":%q,"address":["198.18.19.1/24"],"peers":[]}`, strings.Repeat("32", 32)), "",
			&accountsv1.Credential{Protocol: "wireguard", Peer: &accountsv1.WireGuardPeer{PublicKey: wgKey(10), PreSharedKey: strings.Repeat("53", 32), AllowedIps: []string{"198.18.19.2/32"}}},
			&accountsv1.Credential{Protocol: "wireguard", Peer: &accountsv1.WireGuardPeer{PublicKey: wgKey(11), AllowedIps: []string{"198.18.19.3/32"}}}, 0, wgKey(11)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, _ := freePorts(t)
			stream := ""
			if tc.stream != "" {
				stream = `,"streamSettings":{` + tc.stream + "}"
			}
			source := fmt.Sprintf(`{"inbounds":[{"tag":"subject","listen":"127.0.0.1","port":%d,"protocol":%q,"settings":%s%s}]}`, port, tc.protocol, tc.settings, stream)
			f := startSourceFixture(t, executable, source)
			before := f.nativeUsers(t, "subject")
			if len(before) != tc.wantBefore {
				t.Fatalf("configured membership = %d, want %d", len(before), tc.wantBefore)
			}
			input := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "subject", Email: "sync@example.test", Credential: tc.first}
			result, err := f.grpc.SyncUser(t.Context(), input)
			require(t, err)
			expectSync(t, result, 1, 0)
			if users := f.nativeUsers(t, "subject"); len(users) != tc.wantBefore+1 {
				t.Fatalf("membership after add = %d", len(users))
			}

			invalid := proto.Clone(input).(*accountsv1.SyncUserRequest)
			switch tc.name {
			case "vmess":
				invalid.Credential.Uuid = "malformed"
			case "trojan", "masque":
				invalid.Credential.Password = ""
			case "shadowsocks":
				invalid.Credential.Method = "unsupported-cipher"
			case "shadowsocks2022", "shadowsocks2022-aes256":
				invalid.Credential.Key = "YWJjZA=="
			case "hysteria":
				invalid.Credential.Auth = ""
			case "wireguard":
				invalid.Credential.Peer.AllowedIps = []string{"invalid prefix"}
			}
			if _, err := f.grpc.SyncUser(t.Context(), invalid); status.Code(err) != codes.InvalidArgument {
				t.Fatal("invalid credential was accepted")
			}
			if users := f.nativeUsers(t, "subject"); len(users) != tc.wantBefore+1 {
				t.Fatalf("membership after rejected credential = %d", len(users))
			}
			if tc.name == "wireguard" {
				contender := proto.Clone(input).(*accountsv1.SyncUserRequest)
				contender.Email = "contender"
				contender.Credential.Peer.PublicKey = tc.next.Peer.PublicKey
				contender.Credential.Peer.AllowedIps = []string{"198.18.19.0/24"}
				result, err := f.grpc.SyncUser(t.Context(), contender)
				require(t, err)
				if result.GetOutcome() != "not_applied" || result.GetFailure() != "conflict" {
					t.Fatalf("overlapping WireGuard peer = %v", result)
				}
				assertNamed(t, f, "subject", input.Email)

				noPSK := proto.Clone(input).(*accountsv1.SyncUserRequest)
				noPSK.Credential.Peer.PreSharedKey = ""
				result, err = f.grpc.SyncUser(t.Context(), noPSK)
				require(t, err)
				expectSync(t, result, 1, 1)
				var peer native.WireGuardAccount
				require(t, proto.Unmarshal(f.nativeUsers(t, "subject")[0].Account.Value, &peer))
				if peer.PreSharedKey != "" || peer.PublicKey != input.Credential.Peer.PublicKey {
					t.Fatal("WireGuard peer with the same public key kept its old PSK")
				}
			}

			result, err = f.grpc.SyncUsers(t.Context(), &accountsv1.SyncUsersRequest{Id: instanceID, InboundTag: "subject",
				Snapshot: &accountsv1.VLESSAccounts{Accounts: []*accountsv1.VLESSAccount{{Email: input.Email, Credential: tc.next}}}})
			require(t, err)
			expectSync(t, result, 1, uint32(tc.wantBefore+1))
			users := f.nativeUsers(t, "subject")
			if len(users) != 1 || users[0].Email != input.Email {
				t.Fatalf("membership after snapshot = %d", len(users))
			}
			var firstField native.PasswordAccount
			require(t, proto.Unmarshal(users[0].Account.Value, &firstField))
			if firstField.Password != tc.wantValue {
				t.Fatal("native account retained the wrong credential")
			}
			switch tc.name {
			case "wireguard":
				var peer native.WireGuardAccount
				require(t, proto.Unmarshal(users[0].Account.Value, &peer))
				if peer.PreSharedKey != "" || !slices.Equal(peer.AllowedIps, tc.next.Peer.AllowedIps) {
					t.Fatal("WireGuard peer kept stale settings")
				}
			case "shadowsocks":
				var account native.ShadowsocksAccount
				require(t, proto.Unmarshal(users[0].Account.Value, &account))
				if account.CipherType != 6 {
					t.Fatal("Shadowsocks cipher was not replaced")
				}
			}
			f.request(t, http.MethodDelete, executionPath, nil, http.StatusOK)
			f.request(t, http.MethodPut, executionPath, nil, http.StatusOK)
			restored := f.nativeUsers(t, "subject")
			if len(restored) != 1 || !proto.Equal(restored[0], users[0]) {
				t.Fatal("replacement core did not restore the accepted credential")
			}

			deleteFrame := `{"index":0,"last":true,"accounts":[{"email":"sync@example.test"}]}`
			path := "/v1/xray/instances/primary/inbounds/subject/accounts/membership/stream"
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, f.http.URL+path, strings.NewReader(deleteFrame))
			require(t, err)
			reply, err := f.http.Client().Do(req)
			require(t, err)
			defer reply.Body.Close()
			var sync membership.SyncResult
			require(t, json.NewDecoder(reply.Body).Decode(&sync))
			if reply.StatusCode != http.StatusOK || sync.Outcome != membership.Confirmed || sync.Removed != 1 || len(f.nativeUsers(t, "subject")) != 0 {
				t.Fatalf("native delete was not confirmed: %d %+v", reply.StatusCode, sync)
			}
		})
	}
}

func (f fixture) nativeUsers(t testing.TB, tag string) []*native.User {
	t.Helper()
	conn, err := f.instance.NativeConnection(instanceID)
	require(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reply := new(native.GetInboundUserResponse)
	require(t, conn.Invoke(ctx, native.HandlerService+"GetInboundUsers", &native.GetInboundUserRequest{Tag: tag}, reply))
	return reply.Users
}

func expectSync(t testing.TB, result *accountsv1.SyncResult, added, removed uint32) {
	t.Helper()
	if result.GetOutcome() != string(membership.Confirmed) || result.GetAdded() != added || result.GetRemoved() != removed {
		t.Fatalf("Sync result = %v, want %d additions and %d removals", result, added, removed)
	}
}

func testTLS(t *testing.T) string {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	require(t, err)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, key)
	require(t, err)
	private, err := x509.MarshalPKCS8PrivateKey(key)
	require(t, err)
	lines := func(kind string, data []byte) []string {
		return strings.Split(strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: data}))), "\n")
	}
	config, err := json.Marshal(map[string]any{"certificates": []map[string][]string{{
		"certificate": lines("CERTIFICATE", der), "key": lines("PRIVATE KEY", private),
	}}})
	require(t, err)
	return `"security":"tls","tlsSettings":` + string(config)
}

func TestSyncRejectsSS2022InboundsWithoutNativeUserManager(t *testing.T) {
	executable := os.Getenv("NODE_TEST_XRAY")
	if executable == "" {
		t.Skip("set NODE_TEST_XRAY to the absolute path of Xray v26.11.9")
	}
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 16))
	for _, tc := range []struct{ name, clients string }{
		{"single-user", "[]"},
		{"relay", fmt.Sprintf(`[{"email":"relay","password":%q,"address":"127.0.0.1","port":53}]`, key)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port, _ := freePorts(t)
			source := fmt.Sprintf(`{"inbounds":[{"tag":"subject","listen":"127.0.0.1","port":%d,"protocol":"shadowsocks","settings":{"method":"2022-blake3-aes-128-gcm","password":%q,"clients":%s}}]}`, port, key, tc.clients)
			f := startSourceFixture(t, executable, source)
			reply, err := f.grpc.SyncUser(t.Context(), &accountsv1.SyncUserRequest{
				Id: instanceID, InboundTag: "subject", Email: "member", Credential: &accountsv1.Credential{Protocol: "shadowsocks2022", Key: key},
			})
			require(t, err)
			if reply.GetOutcome() != "not_applied" || reply.GetFailure() != "wrong_protocol" {
				t.Fatalf("%s received a live account effect: %v", tc.name, reply)
			}
		})
	}
}
