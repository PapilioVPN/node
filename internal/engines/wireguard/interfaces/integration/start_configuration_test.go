package integration_test

import (
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestInitialPeersAndLiveConfiguration(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("requires an isolated network namespace")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "-Urn", os.Args[0], "-test.run=^TestInitialPeersAndLiveConfiguration$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated configuration: %v\n%s", err, out)
		}
		return
	}
	f := startFixture(t, true)
	first, second, peer := [32]byte{1, 1}, [32]byte{2, 2}, [32]byte{3, 3}
	cfg := struct {
		PrivateKey   []byte   `json:"private_key"`
		Addresses    []string `json:"addresses"`
		ListenPort   int      `json:"listen_port"`
		MTU          uint32   `json:"mtu,omitempty"`
		InitialPeers []struct {
			PublicKey    []byte   `json:"public_key"`
			PresharedKey []byte   `json:"preshared_key"`
			AllowedIPs   []string `json:"allowed_ips"`
		} `json:"initial_peers"`
	}{PrivateKey: first[:], Addresses: []string{"192.0.2.1/24"}}
	psk := [32]byte{4, 4}
	cfg.InitialPeers = append(cfg.InitialPeers, struct {
		PublicKey    []byte   `json:"public_key"`
		PresharedKey []byte   `json:"preshared_key"`
		AllowedIPs   []string `json:"allowed_ips"`
	}{peer[:], psk[:], []string{"192.0.2.2/32"}})
	apply := func() {
		body, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := executionRequest(t, f, http.MethodPut, body, http.StatusOK); !got.Up || got.AppliedSHA256 == "" {
			t.Fatalf("configuration not confirmed: %+v", got)
		}
	}
	apply()
	assertPeerMembership(t, f, peer)
	if !strings.Contains(wgOutput(t, "allowed-ips"), base64.StdEncoding.EncodeToString(peer[:])+"\t192.0.2.2/32") {
		t.Fatal("Start returned before initial peers were applied")
	}
	before, err := net.InterfaceByName(interfaceName)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenPort = listener.LocalAddr().(*net.UDPAddr).Port
	listener.Close()
	apply()
	if strings.TrimSpace(wgOutput(t, "listen-port")) != strconv.Itoa(cfg.ListenPort) {
		t.Fatal("listen port was not applied")
	}
	cfg.Addresses = []string{"192.0.2.10/24"}
	apply()
	cfg.PrivateKey = second[:]
	apply()
	key, _ := ecdh.X25519().NewPrivateKey(second[:])
	assertInterfaceConfiguration(t, f, key.PublicKey().Bytes(), cfg.ListenPort, cfg.Addresses[0])
	if strings.TrimSpace(wgOutput(t, "public-key")) != base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()) {
		t.Fatal("public key was not rotated")
	}
	after, err := net.InterfaceByName(interfaceName)
	if err != nil || after.Index != before.Index {
		t.Fatalf("link was replaced: %v, %v", after, err)
	}
	addresses, err := after.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, addr := range addresses {
		if addr.String() == "192.0.2.10/24" {
			found = true
		}
		if addr.String() == "192.0.2.1/24" {
			t.Fatal("old address survived")
		}
	}
	if !found || !strings.Contains(wgOutput(t, "peers"), base64.StdEncoding.EncodeToString(peer[:])) {
		t.Fatal("address change or peer preservation failed")
	}

	reserve, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	cfg.ListenPort = reserve.LocalAddr().(*net.UDPAddr).Port
	reserve.Close()
	cfg.MTU, cfg.Addresses = 1300, []string{"192.0.2.20/24"}
	cfg.PrivateKey = first[:]
	apply()
	key, _ = ecdh.X25519().NewPrivateKey(first[:])
	assertInterfaceConfiguration(t, f, key.PublicKey().Bytes(), cfg.ListenPort, cfg.Addresses[0])
	confirmed := f.readHTTP(t)
	link, err := net.InterfaceByName(interfaceName)
	if err != nil || link.Index != before.Index || link.MTU != 1300 {
		t.Fatalf("compound change did not retain the link and apply MTU: %v, %v", link, err)
	}
	assertPeerMembership(t, f, peer)

	blocked, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	previousPort := cfg.ListenPort
	cfg.ListenPort = blocked.LocalAddr().(*net.UDPAddr).Port
	cfg.MTU, cfg.Addresses = 1350, []string{"192.0.2.30/24"}
	failed, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	executionRequest(t, f, http.MethodPut, failed, http.StatusServiceUnavailable)
	afterFailure := f.readHTTP(t)
	if afterFailure.MTU != 1300 || afterFailure.AppliedSHA256 != confirmed.AppliedSHA256 || afterFailure.AppliedSHA256 == "" {
		t.Fatalf("rejected compound change altered the confirmed configuration: %+v", afterFailure)
	}
	assertInterfaceConfiguration(t, f, key.PublicKey().Bytes(), previousPort, "192.0.2.20/24")
	if got := strings.Join(ipv4Addresses(t, link), ","); got != "192.0.2.20/24" {
		t.Fatalf("address rollback left %s", got)
	}
}

func ipv4Addresses(t *testing.T, link *net.Interface) []string {
	t.Helper()
	addresses, err := link.Addrs()
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, address := range addresses {
		if value, ok := address.(*net.IPNet); ok && value.IP.To4() != nil {
			result = append(result, value.String())
		}
	}
	return result
}
