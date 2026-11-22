//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
)

var image = flag.String("image", "", "prebuilt checked Node image")

func TestPackagedAccountReads(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	pki := testutil.NewPKI(t)
	target := accountTarget(t)
	f := newFixture(t, pki, brokerFixture(t, pki), "all")
	address := allowAccountTraffic(t, f, target.Listener.Addr().String())
	work := t.TempDir()
	f.config.Instances[0].Xray.WorkDirectory = work
	f.config.Communications.Requests.Timeout = 2 * time.Second
	f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
	config := f.config
	config.Instances = append(config.Instances[:0:0], config.Instances...)
	xray := *config.Instances[0].Xray
	config.Instances[0].Xray = &xray
	xray.Binary = "/opt/xray"
	xray.WorkDirectory = "/work"
	xray.Configuration = "/etc/engine/config.json"
	relative := func(files mtls.Files) mtls.Files {
		return mtls.Files{Certificate: filepath.Base(files.Certificate), PrivateKey: filepath.Base(files.PrivateKey), CA: filepath.Base(files.CA)}
	}
	h, g, n := *config.Communications.HTTP, *config.Communications.GRPC, *config.Communications.NATS
	h.TLS = relative(h.TLS)
	g.TLS = relative(g.TLS)
	n.TLS = relative(n.TLS)
	config.Communications.HTTP = &h
	config.Communications.GRPC = &g
	config.Communications.NATS = &n
	directory := filepath.Dir(pki.Server.Certificate)
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yml"), data, 0600); err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "node-accounts-" + hex.EncodeToString(random[:])
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src="+directory+",dst=/etc/node,readonly",
		"--mount", "type=bind,src="+work+",dst=/work",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Binary+",dst=/opt/xray,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Configuration+",dst=/etc/engine/config.json,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	client := connect(t, f, containerAddresses(t, name))
	awaitNative(t, f.native)
	for _, transport := range []string{"http", "grpc", "nats"} {
		assertNativeAccounts(t, client, transport)
		exerciseAccountControl(t, client, transport, address, target.Listener.Addr().String())
		exerciseSourceAccount(t, client, transport, "json", address, target.Listener.Addr().String())
		exercisePackagedBatch(t, client, transport, address, target.Listener.Addr().String())
		prepared := prepareAccounts(t, client, transport, []byte(`{"inbounds":[{"tag":"members","port":1,"protocol":"vless","settings":{"decryption":"none","clients":[]}}]}`), [][]byte{accountConfiguration(t, "prepared@example.test")})
		hash := sha256.Sum256(prepared.Content)
		if len(prepared.Content) == 0 || prepared.SHA256 != hex.EncodeToString(hash[:]) || prepared.InstanceID != "edge-a" {
			t.Fatal("packaged preparation returned invalid artifact", prepared.SHA256)
		}
		assertFamilyCount(t, client, transport, 2)
	}

	docker(t, "stop", "--time", "8", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged account observation owner did not shut down cleanly")
	}
	connection, err := net.DialTimeout("tcp", f.native, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("native Xray API survived Node shutdown")
	}
}

func exercisePackagedBatch(t *testing.T, client clients, transport, address, target string) {
	t.Helper()
	second, err := proto.Marshal(&protocol.User{Email: "second-batch@example.test", Account: serial.ToTypedMessage(&vless.Account{Id: "bb8cb7d1-537e-484e-ae0e-02c28c8aad02"})})
	if err != nil {
		t.Fatal(err)
	}
	result := batchAccounts(t, client, transport, []domain.Addition{
		{InboundTag: "members", Configuration: accountConfiguration(t, "first-batch@example.test")},
		{InboundTag: "members", Configuration: second},
	}, nil, 4)
	if result.Error != "" || len(result.Items) != 2 || result.Operation.Effect != "completed" {
		t.Fatal(result)
	}
	assertFamilyCount(t, client, transport, 4)
	connection, _, err := accountTunnel(address, managedID, target)
	if err != nil {
		t.Fatal("packaged batch account did not authenticate", err)
	}
	connection.Close()
	result = batchAccounts(t, client, transport, nil, []domain.Removal{
		{InboundTag: "members", Email: "first-batch@example.test"},
		{InboundTag: "members", Email: "second-batch@example.test"},
	}, 4)
	if result.Error != "" || result.Operation.Effect != "completed" {
		t.Fatal(result)
	}
	assertFamilyCount(t, client, transport, 2)
	if connection, _, err := accountTunnel(address, managedID, target); err == nil {
		connection.Close()
		t.Fatal("packaged batch removal did not revoke new authentication")
	}
}
func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", args...)
	command.WaitDelay = time.Second
	data, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v %s", args[0], err, data)
	}
	return string(data)
}
func containerAddresses(t *testing.T, name string) map[string]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		addresses := map[string]string{}
		subscribed := false
		for line := range strings.SplitSeq(docker(t, "logs", name), "\n") {
			var event struct {
				Message string `json:"msg"`
				Address string `json:"address"`
			}
			if json.Unmarshal([]byte(line), &event) != nil {
				continue
			}
			if event.Message == "HTTP listening" || event.Message == "gRPC listening" {
				addresses[event.Message] = event.Address
			}
			if event.Message == "Xray account lookup NATS subscribed" {
				subscribed = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && subscribed {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged account endpoints did not start")
		case <-ticker.C:
		}
	}
}
