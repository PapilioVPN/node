//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func publicKey(seed byte) []byte {
	key, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{seed}, 32))
	if err != nil {
		panic(err)
	}
	return key.PublicKey().Bytes()
}

// peer uses the pinned PeerConfig wire ABI without importing its co-located
// engine/server implementation and unrelated runtime dependencies into Node.
func peer(seed, psk byte) []byte {
	var content []byte
	field := func(n protowire.Number, value string) {
		content = protowire.AppendTag(content, n, protowire.BytesType)
		content = protowire.AppendString(content, value)
	}
	field(1, hex.EncodeToString(publicKey(seed)))
	if psk != 0 {
		field(2, hex.EncodeToString(bytes.Repeat([]byte{psk}, 32)))
	}
	field(5, "10.55.0.2/32")
	user, _ := proto.Marshal(&protocol.User{Email: "peer", Account: &serial.TypedMessage{Type: "xray.proxy.wireguard.PeerConfig", Value: content}})
	return user
}
func wgTraffic(t *testing.T, f *fullFixture, endpoint string, seed, psk byte) error {
	t.Helper()
	listener := listen(t)
	port := listener.Addr().(*net.TCPAddr).Port
	proxyAddress := listener.Addr().String()
	remote := map[string]any{"publicKey": base64.StdEncoding.EncodeToString(publicKey(1)), "endpoint": endpoint, "allowedIPs": []string{"0.0.0.0/0"}}
	if psk != 0 {
		remote["preSharedKey"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{psk}, 32))
	}
	configuration := map[string]any{"log": map[string]any{"loglevel": "none"}, "inbounds": []any{map[string]any{"listen": "127.0.0.1", "port": port, "protocol": "http", "settings": map[string]any{}}}, "outbounds": []any{map[string]any{"protocol": "wireguard", "settings": map[string]any{"noKernelTun": true, "secretKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{seed}, 32)), "address": []string{"10.55.0.2/32"}, "peers": []any{remote}}}}}
	data, _ := json.Marshal(configuration)
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	runtime, err := instanceprocess.New(instanceprocess.Config{Binary: f.binary, Configuration: path, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtime.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	for {
		conn, err := net.DialTimeout("tcp", proxyAddress, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	proxy, _ := url.Parse("http://" + proxyAddress)
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 700 * time.Millisecond}
	_, targetPort, _ := net.SplitHostPort(f.target)
	response, err := client.Get("http://198.18.0.1:" + targetPort)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 32))
	if err != nil || string(body) != "sync-ok" {
		return fmt.Errorf("WireGuard payload failed: %v", err)
	}
	return nil
}

func TestNativeWireGuardKeyPSKReplacementAndLostRemoval(t *testing.T) {
	f := withFull(t, newFixture(t))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tcp := listen(t)
	endpoint := tcp.Addr().String()
	udp, err := net.ListenPacket("udp", endpoint)
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	content, err := os.ReadFile(f.baseline)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err = json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	configuration["inbounds"] = append(configuration["inbounds"].([]any), map[string]any{"tag": "wireguard", "listen": "127.0.0.1", "port": tcp.Addr().(*net.TCPAddr).Port, "protocol": "wireguard", "settings": map[string]any{"secretKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32)), "address": []string{"10.55.0.1/24"}, "peers": []any{map[string]any{"email": "peer", "publicKey": base64.StdEncoding.EncodeToString(publicKey(2)), "preSharedKey": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32)), "allowedIPs": []string{"10.55.0.2/32"}}}}})
	configuration["outbounds"].([]any)[0].(map[string]any)["settings"].(map[string]any)["redirect"] = f.target
	content, _ = json.Marshal(configuration)
	if err = os.WriteFile(f.baseline, content, 0600); err != nil {
		t.Fatal(err)
	}
	tcp.Close()
	udp.Close()
	if _, err = f.instances.Restart(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	for {
		state, err := f.instances.Check(ctx, "one")
		if err == nil && state.APIState == "ready" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := wgTraffic(t, f, endpoint, 2, 8); err != nil {
		t.Fatal("initial PSK", err)
	}
	apply := func(id string, seed, psk byte) {
		t.Helper()
		revision, err := f.service.Revision(ctx, "one")
		if err != nil {
			t.Fatal(err)
		}
		r := domain.Request{InstanceID: "one", OperationID: id, Expected: revision, Mode: domain.Delta, Selection: []string{"wireguard"}, Sets: []accounts.InboundUserSet{{InboundTag: "wireguard", Accounts: [][]byte{peer(seed, psk)}}}}
		p, err := f.service.Preview(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		result, err := f.service.Apply(ctx, "one", p.PlanID, p.Revision)
		if err != nil || len(result.Receipt.Steps) != 2 || result.Receipt.Steps[0].Action != domain.Remove || result.Receipt.Steps[1].Action != domain.Add {
			t.Fatal(result, err)
		}
		observed, err := f.accounts.Count(ctx, "one", "wireguard")
		if err != nil || observed.Count != 1 {
			t.Fatal("old peer membership retained", observed, err)
		}
	}
	apply("psk", 2, 9)
	if wgTraffic(t, f, endpoint, 2, 8) == nil {
		t.Fatal("old PSK authenticated")
	}
	if err := wgTraffic(t, f, endpoint, 2, 9); err != nil {
		t.Fatal("new PSK", err)
	}
	apply("key-and-clear-psk", 3, 0)
	if wgTraffic(t, f, endpoint, 2, 9) == nil {
		t.Fatal("old public key authenticated")
	}
	if err := wgTraffic(t, f, endpoint, 3, 0); err != nil {
		t.Fatal("new key without retained PSK", err)
	}
	revision, _ := f.service.Revision(ctx, "one")
	p, err := f.service.Preview(ctx, domain.Request{InstanceID: "one", OperationID: "failed-remove", Expected: revision, Mode: domain.Delta, Selection: []string{"wireguard"}, Sets: []accounts.InboundUserSet{{InboundTag: "wireguard", Accounts: [][]byte{peer(4, 0)}}}})
	if err != nil {
		t.Fatal(err)
	}
	relay, _ := testutil.LostReplies(t, f.native)
	connection, err := rpc.New(rpc.Config{Network: "tcp", Address: relay})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	f.calls.mu.Lock()
	f.calls.mutation = connection
	f.calls.mu.Unlock()
	short, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	result, err := f.service.Apply(short, "one", p.PlanID, p.Revision)
	stop()
	if err == nil || result.Receipt.Effect != accounts.Unknown || result.Receipt.Steps[1].Attempted {
		t.Fatal(result, err)
	}
	if wgTraffic(t, f, endpoint, 4, 0) == nil {
		t.Fatal("replacement was added after failed removal")
	}
}
