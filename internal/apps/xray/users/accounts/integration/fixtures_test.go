//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	xrayprocess "github.com/PapilioVPN/node/internal/apps/core/engines/instances/infra/xray/process"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

type configurationFixture struct {
	instance                     settings.Instance
	original, compiled           []byte
	users                        []domain.InboundUserSet
	health, oldProxy, newProxy   string
	untouchedProxy, clientConfig string
	tcp, udp                     []string
	reservations                 []io.Closer
}

func nativeConfiguration(t *testing.T) *configurationFixture {
	t.Helper()
	certificates := testutil.NewCertificates(t)
	f := new(configurationFixture)
	f.instance, f.health = testutil.ConfiguredXray(t, certificates.Directory, "prepared")
	f.reserve(t, "tcp", f.health)
	tls := certificates.Config().Communications.GRPC.TLS
	stream := func(network, alpn string) map[string]any {
		settings := map[string]any{"network": network, "security": "tls", "tlsSettings": map[string]any{
			"alpn": []string{alpn}, "certificates": []map[string]any{{"certificateFile": tls.CertificateFile, "keyFile": tls.PrivateKeyFile, "oneTimeLoading": true}},
		}}
		if network == "hysteria" {
			settings["hysteriaSettings"] = map[string]any{"version": 2}
		}
		return settings
	}
	key := func(value byte, size int) string {
		return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{value}, size))
	}
	const oldID = "8e781ad6-dae6-4ed4-9d7b-8c247f735869"
	const newID = "4caa3ce4-39ab-49df-bc05-624befa32996"
	cases := []struct {
		tag, protocol, network string
		settings, stream       map[string]any
		account                domain.UserAccount
	}{
		{"vless", "vless", "tcp", map[string]any{"decryption": "none", "clients": []map[string]any{{"id": oldID}}}, nil,
			domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: newID}}},
		{"vmess", "vmess", "tcp", map[string]any{"clients": []map[string]any{{"id": oldID}}}, nil,
			domain.UserAccount{Protocol: domain.VMess, VMess: &domain.VMessAccount{ID: newID}}},
		{"trojan", "trojan", "tcp", map[string]any{"clients": []map[string]any{{"password": "original-trojan"}}}, nil,
			domain.UserAccount{Protocol: domain.Trojan, Trojan: &domain.PasswordAccount{Password: "desired-trojan"}}},
		{"shadowsocks", "shadowsocks", "tcp", map[string]any{"clients": []map[string]any{{"method": "aes-128-gcm", "password": "original-ss"}}}, nil,
			domain.UserAccount{Protocol: domain.Shadowsocks, Shadowsocks: &domain.ShadowsocksAccount{Cipher: domain.AES128GCM, Password: "desired-ss", IVCheck: true}}},
		{"ss2022", "shadowsocks", "tcp", map[string]any{"method": "2022-blake3-aes-128-gcm", "password": key(9, 16), "clients": []map[string]any{{"password": key(8, 16)}}}, nil,
			domain.UserAccount{Protocol: domain.Shadowsocks2022, Shadowsocks2022: &domain.Shadowsocks2022Account{Key: bytes.Repeat([]byte{7}, 16)}}},
		{"hysteria", "hysteria", "udp", map[string]any{"version": 2, "clients": []map[string]any{{"auth": "original-auth"}}}, stream("hysteria", "h3"),
			domain.UserAccount{Protocol: domain.Hysteria, Hysteria: &domain.HysteriaAccount{Auth: "desired-auth"}}},
		{"masque", "masque", "tcp", map[string]any{"address": []string{"10.254.1.1/30"}, "clients": []map[string]any{{"pass": "original-masque"}}}, stream("masque", "h2"),
			domain.UserAccount{Protocol: domain.MASQUE, MASQUE: &domain.PasswordAccount{Password: "desired-masque"}}},
		{"wireguard", "wireguard", "udp", map[string]any{"secretKey": key(9, 32), "address": []string{"10.254.2.1/24"}, "noKernelTun": true, "peers": []map[string]any{{"publicKey": key(6, 32), "allowedIPs": []string{"10.254.2.3/32"}}}}, nil,
			domain.UserAccount{Protocol: domain.WireGuard, WireGuard: &domain.WireGuardAccount{PublicKey: bytes.Repeat([]byte{7}, 32), PreSharedKey: bytes.Repeat([]byte{8}, 32), AllowedIPs: []string{"10.254.2.2/32"}, KeepAliveSeconds: new(uint32)}}},
	}
	var config map[string]any
	content, err := os.ReadFile(f.instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	config["outbounds"] = []map[string]any{{"protocol": "freedom", "settings": map[string]any{"finalRules": []map[string]any{{"action": "allow", "ip": []string{"127.0.0.1/32"}}}}}}
	var vlessPort int
	for _, tc := range cases {
		port := f.port(t, tc.network)
		field := "clients"
		if tc.tag == "wireguard" {
			field = "peers"
		}
		tc.settings[field].([]map[string]any)[0]["email"] = "original@local"
		tc.settings[field].([]map[string]any)[0]["level"] = 3
		inbound := map[string]any{"tag": tc.tag, "listen": "127.0.0.1", "port": port, "protocol": tc.protocol, "settings": tc.settings}
		if tc.stream != nil {
			inbound["streamSettings"] = tc.stream
		}
		config["inbounds"] = append(config["inbounds"].([]any), inbound)
		if tc.tag == "vless" {
			vlessPort = port
		}
		if tc.tag == "shadowsocks" || tc.tag == "ss2022" {
			f.reserve(t, "udp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		}
		f.users = append(f.users, domain.InboundUserSet{InboundTag: tc.tag, Users: []domain.InboundUser{{Email: "desired@local", Level: 7, Account: tc.account}}})
	}
	untouchedPort := f.port(t, "tcp")
	config["inbounds"] = append(config["inbounds"].([]any), map[string]any{"tag": "untouched", "listen": "127.0.0.1", "port": untouchedPort, "protocol": "vless",
		"settings": map[string]any{"decryption": "none", "clients": []map[string]any{{"id": oldID, "email": "untouched@local"}}}})
	writeJSON(t, f.instance.Xray.ConfigFile, config)
	testutil.ConfigureXrayAPI(t, &f.instance, "unix", []string{"HandlerService"})
	f.instance.Xray.API.MaxReceiveBytes = 64 * 1024
	f.original, err = os.ReadFile(f.instance.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	f.compiled = compileConfiguration(t, f.instance)
	f.clientConfig = filepath.Join(certificates.Directory, "client.json")
	var inbounds, outbounds, rules []map[string]any
	var proxies []string
	for i, target := range []struct {
		port int
		id   string
	}{{vlessPort, oldID}, {vlessPort, newID}, {untouchedPort, oldID}} {
		port := f.port(t, "tcp")
		in, out := fmt.Sprint("client-in-", i), fmt.Sprint("client-out-", i)
		proxies = append(proxies, net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
		inbounds = append(inbounds, map[string]any{"tag": in, "listen": "127.0.0.1", "port": port, "protocol": "http", "settings": map[string]any{}})
		outbounds = append(outbounds, map[string]any{"tag": out, "protocol": "vless", "settings": map[string]any{"vnext": []map[string]any{{"address": "127.0.0.1", "port": target.port, "users": []map[string]string{{"id": target.id, "encryption": "none"}}}}}})
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{in}, "outboundTag": out})
	}
	writeJSON(t, f.clientConfig, map[string]any{"log": map[string]string{"loglevel": "none"}, "inbounds": inbounds, "outbounds": outbounds, "routing": map[string]any{"rules": rules}})
	f.oldProxy, f.newProxy, f.untouchedProxy = proxies[0], proxies[1], proxies[2]
	return f
}

func compileConfiguration(t *testing.T, instance settings.Instance) []byte {
	t.Helper()
	directory := filepath.Dir(instance.Xray.ConfigFile)
	compiledPath := filepath.Join(directory, "original.pb")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, instance.Xray.Binary, "convert", "pb", "-o", compiledPath, instance.Xray.ConfigFile)
	command.Dir, command.Env = directory, []string{}
	command.Stdout, command.Stderr = io.Discard, io.Discard
	command.WaitDelay = time.Second
	if err := command.Run(); err != nil {
		t.Fatalf("native fixture conversion failed: %v", err)
	}
	compiled, err := os.ReadFile(compiledPath)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func (f *configurationFixture) reserve(t *testing.T, network, address string) int {
	t.Helper()
	var closer io.Closer
	var port int
	if network == "tcp" {
		listener, err := net.Listen(network, address)
		if err != nil {
			t.Fatal(err)
		}
		closer, port, address = listener, listener.Addr().(*net.TCPAddr).Port, listener.Addr().String()
		f.tcp = append(f.tcp, address)
	} else {
		listener, err := net.ListenPacket(network, address)
		if err != nil {
			t.Fatal(err)
		}
		closer, port, address = listener, listener.LocalAddr().(*net.UDPAddr).Port, listener.LocalAddr().String()
		f.udp = append(f.udp, address)
	}
	f.reservations = append(f.reservations, closer)
	t.Cleanup(func() { _ = closer.Close() })
	return port
}

func (f *configurationFixture) port(t *testing.T, network string) int {
	t.Helper()
	return f.reserve(t, network, "127.0.0.1:0")
}

func (f *configurationFixture) startClient(t *testing.T) {
	t.Helper()
	client, err := xrayprocess.New(xrayprocess.Config{Binary: f.instance.Xray.Binary, ConfigFile: f.clientConfig, StopTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, reservation := range f.reservations {
		if err := reservation.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (f *configurationFixture) assertReleased(t *testing.T) {
	t.Helper()
	testutil.AssertTCPPortsReleased(t, f.tcp...)
	for _, address := range f.udp {
		listener, err := net.ListenPacket("udp", address)
		if err != nil {
			t.Errorf("owned UDP listener leaked: %v", err)
			continue
		}
		_ = listener.Close()
	}
	if _, err := os.Lstat(f.instance.Xray.API.Address); !os.IsNotExist(err) {
		t.Errorf("owned Unix listener was not removed: %v", err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}
