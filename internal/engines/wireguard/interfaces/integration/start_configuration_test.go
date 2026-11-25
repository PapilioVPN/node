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
		InitialPeers []struct {
			PublicKey  []byte   `json:"public_key"`
			AllowedIPs []string `json:"allowed_ips"`
		} `json:"initial_peers"`
	}{PrivateKey: first[:], Addresses: []string{"192.0.2.1/24"}}
	cfg.InitialPeers = append(cfg.InitialPeers, struct {
		PublicKey  []byte   `json:"public_key"`
		AllowedIPs []string `json:"allowed_ips"`
	}{peer[:], []string{"192.0.2.2/32"}})
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
}
