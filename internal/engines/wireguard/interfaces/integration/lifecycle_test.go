package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
)

func TestManagedExecution(t *testing.T) {
	if os.Getenv(parentNetNS) == "" {
		if os.Getenv("NODE_TEST_WIREGUARD") != "1" {
			t.Skip("set NODE_TEST_WIREGUARD=1 for the isolated Linux kernel test")
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--user", "--map-root-user", "--net",
			os.Args[0], "-test.run=^TestManagedExecution$")
		cmd.Env = append(os.Environ(), parentNetNS+"="+networkNamespace(t))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated WireGuard test: %v\n%s", err, out)
		}
		return
	}
	f := startFixture(t, true)
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	request := struct {
		PrivateKey []byte   `json:"private_key"`
		Addresses  []string `json:"addresses"`
		MTU        uint32   `json:"mtu"`
		ListenPort int      `json:"listen_port,omitempty"`
	}{PrivateKey: privateKey.Bytes(), Addresses: []string{"192.0.2.1/24"}, MTU: 1380}
	source, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	want := hex.EncodeToString(digest[:])
	client := f.grpcClient(t)
	stats := trafficClient(t, f)

	runIP(t, "link", "add", "dev", interfaceName, "type", "wireguard")
	executionRequest(t, f, http.MethodPut, source, http.StatusConflict)
	executionRequest(t, f, http.MethodDelete, nil, http.StatusConflict)
	runIP(t, "link", "del", "dev", interfaceName)
	if got := f.validateHTTP(t, interfaceName, source, http.StatusOK); !got.Valid {
		t.Fatalf("validation before Start = %+v", got)
	}
	if _, err := net.InterfaceByName(interfaceName); err == nil {
		t.Fatal("validation created an interface")
	}
	executionRequest(t, f, http.MethodPut, []byte("{}"), http.StatusBadRequest)
	executionRequest(t, f, http.MethodPut, bytes.Repeat([]byte(" "), lifecycle.MaxConfigBytes+1), http.StatusRequestEntityTooLarge)

	reply := executionRequest(t, f, http.MethodPut, source, http.StatusOK)
	if reply.Name != interfaceName || reply.MTU != 1380 || !reply.Up || reply.AppliedSHA256 != want {
		t.Fatalf("unexpected execution status: %+v", reply)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	rpcReply, err := client.Start(ctx, &interfacesv1.WireGuardStartRequest{Name: interfaceName, ConfigJson: source})
	cancel()
	if err != nil || rpcReply.GetAppliedConfigSha256() != want || !rpcReply.GetUp() {
		t.Fatalf("gRPC heartbeat returned %v, %v", rpcReply, err)
	}
	if got := f.readHTTP(t); !got.Up || got.AppliedSHA256 != want {
		t.Fatalf("unexpected readback: %+v", got)
	}
	if got := readGRPC(t, client); !got.Up || got.AppliedSHA256 != want {
		t.Fatalf("unexpected gRPC readback: %+v", got)
	}
	if peers := readPeerStatsHTTP(t, f).Peers; peers == nil || len(peers) != 0 {
		t.Fatalf("new interface statistics = %v, want an empty list", peers)
	}
	assertInterfaceTraffic(t, f, stats, false)
	assertMissingPeerStats(t, client, "wg-other")
	assertValidation(t, f, client, append(bytes.Clone(source), '\n'), bytes.Replace(source, []byte(`"mtu":1380`), []byte(`"mtu":1`), 1), want)
	configured, err := exec.CommandContext(t.Context(), "wg", "show", interfaceName, "listen-port").Output()
	if err != nil || strings.TrimSpace(string(configured)) == "0" {
		t.Fatalf("expected a bound WireGuard port, got %q (%v)", configured, err)
	}
	exchangePacket(t, base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()), strings.TrimSpace(string(configured)), func(peer string, ping func()) {
		assertActivePeerStats(t, f, client, peer)
		assertPeerTraffic(t, f, stats, peer, ping)
		assertInterfaceTraffic(t, f, stats, true)
		assertPeerSync(t, f, client, peer)
		assertPeerDelta(t, f, client, privateKey.PublicKey().Bytes())
		before, err := net.InterfaceByName(interfaceName)
		if err != nil {
			t.Fatal(err)
		}
		request.MTU = 1360
		source, err = json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		digest = sha256.Sum256(source)
		want = hex.EncodeToString(digest[:])
		if got := executionRequest(t, f, http.MethodPut, source, http.StatusOK); got.MTU != 1360 || !got.Up || got.AppliedSHA256 != want {
			t.Fatalf("live MTU update returned %+v", got)
		}
		request.MTU = 1340
		source, err = json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		digest = sha256.Sum256(source)
		want = hex.EncodeToString(digest[:])
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		applied, err := client.Start(ctx, &interfacesv1.WireGuardStartRequest{Name: interfaceName, ConfigJson: source})
		cancel()
		if err != nil || applied.GetMtu() != 1340 || !applied.GetUp() || applied.GetAppliedConfigSha256() != want {
			t.Fatalf("gRPC live MTU update returned %v, %v", applied, err)
		}
		after, err := net.InterfaceByName(interfaceName)
		if err != nil || after.Index != before.Index || after.MTU != 1340 {
			t.Fatalf("live MTU changed the interface identity or missed the update: %v, %v", after, err)
		}
		unsupported := bytes.Replace(source, []byte(`"mtu":1340`), []byte(`"mtu":1320,"listen_port":51820`), 1)
		executionRequest(t, f, http.MethodPut, unsupported, http.StatusConflict)
		if got := readGRPC(t, client); got.MTU != 1340 || got.AppliedSHA256 != want {
			t.Fatalf("rejected port change changed the active config: %+v", got)
		}
	})
	assertBulkPeerSync(t, f, client)
	executionRequest(t, f, http.MethodPut, append(bytes.Clone(source), '\n'), http.StatusConflict)
	if got := f.readHTTP(t); got.AppliedSHA256 != want {
		t.Fatalf("rejected change replaced applied hash: %+v", got)
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	_, err = client.Stop(ctx, &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName})
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	executionRequest(t, f, http.MethodDelete, nil, http.StatusNoContent)
	if err := exec.CommandContext(t.Context(), "ip", "link", "show", "dev", interfaceName).Run(); err == nil {
		t.Fatal("managed interface survived Stop")
	}
	assertMissingPeerStats(t, client, interfaceName)
	assertMissingInterfaceTraffic(t, f, stats, interfaceName)
	blocked, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	defer blocked.Close()
	request.ListenPort = blocked.LocalAddr().(*net.UDPAddr).Port
	conflict, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	executionRequest(t, f, http.MethodPut, conflict, http.StatusServiceUnavailable)
	if _, err := net.InterfaceByName(interfaceName); err == nil {
		t.Fatal("failed Start retained the managed interface")
	}
	executionRequest(t, f, http.MethodPut, source, http.StatusOK)
	executionRequest(t, f, http.MethodDelete, nil, http.StatusNoContent)
}

func exchangePacket(t *testing.T, serverKey, serverPort string, whileConnected func(string, func())) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, "unshare", "--net", "sh", "-c", "echo ready; read -r hold")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		_ = child.Wait()
	}()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("client namespace: %q, %v", line, err)
	}
	pid := fmt.Sprint(child.Process.Pid)
	runIP(t, "link", "add", "wg-underlay", "type", "veth", "peer", "name", "wg-peer-link", "netns", pid)
	runIP(t, "addr", "add", "198.51.100.1/30", "dev", "wg-underlay")
	runIP(t, "link", "set", "dev", "wg-underlay", "up")
	run := func(stdin string, argv ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v: %s", argv[0], err, out)
		}
	}
	inClient := func(stdin string, argv ...string) {
		t.Helper()
		run(stdin, append([]string{"nsenter", "--target", pid, "--net", "--"}, argv...)...)
	}
	clientKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientPub := base64.StdEncoding.EncodeToString(clientKey.PublicKey().Bytes())
	run("", "wg", "set", interfaceName, "peer", clientPub, "allowed-ips", "192.0.2.2/32")
	inClient("", "ip", "addr", "add", "198.51.100.2/30", "dev", "wg-peer-link")
	inClient("", "ip", "link", "set", "dev", "wg-peer-link", "up")
	inClient("", "ip", "link", "add", "wg-client", "type", "wireguard")
	inClient("", "ip", "addr", "add", "192.0.2.2/24", "dev", "wg-client")
	config := fmt.Sprintf("[Interface]\nPrivateKey = %s\n[Peer]\nPublicKey = %s\nAllowedIPs = 192.0.2.1/32\nEndpoint = 198.51.100.1:%s\n",
		base64.StdEncoding.EncodeToString(clientKey.Bytes()), serverKey, serverPort)
	inClient(config, "wg", "setconf", "wg-client", "/dev/stdin")
	inClient("", "ip", "link", "set", "dev", "wg-client", "up")
	ping := func() { inClient("", "ping", "-n", "-c", "1", "-W", "2", "192.0.2.1") }
	ping()
	whileConnected(clientPub, ping)
	ping()
}

func executionRequest(t testing.TB, f fixture, method string, body []byte, want int) observation {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.address+interfaceURL+"/execution", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		t.Fatalf("%s execution returned %s, want %d", method, resp.Status, want)
	}
	var result observation
	if want == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
	}
	return result
}
