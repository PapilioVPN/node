//go:build integration && distribution && linux

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
)

var image = flag.String("image", "", "prebuilt checked Node image")

func TestPackagedOutboundReadback(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must select the prebuilt image")
	}
	pki := testutil.NewPKI(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "outbound-traffic") }))
	defer target.Close()
	f := newFixture(t, pki, brokerFixture(t, pki), "all")
	probeAddress := configureProbe(t, f, true)
	workDirectory := t.TempDir()
	f.config.Instances[0].Xray.WorkDirectory = workDirectory
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
	name := "node-outbounds-" + hex.EncodeToString(random[:])
	f.resources.release()
	docker(t, "run", "--detach", "--pull=never", "--name", name, "--network=host", "--cap-drop=ALL", "--read-only", "--user", strconv.Itoa(os.Getuid())+":"+strconv.Itoa(os.Getgid()),
		"--mount", "type=bind,src="+directory+",dst=/etc/node,readonly",
		"--mount", "type=bind,src="+workDirectory+",dst=/work",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Binary+",dst=/opt/xray,readonly",
		"--mount", "type=bind,src="+f.config.Instances[0].Xray.Configuration+",dst=/etc/engine/config.json,readonly", *image)
	t.Cleanup(func() { docker(t, "rm", "--force", name) })
	client := connect(t, f, containerAddresses(t, name))
	awaitNative(t, f.native)
	for _, transport := range []string{"http", "grpc", "nats"} {
		result := readOutbounds(t, client, transport)
		if len(result.Outbounds) != 1 || result.Outbounds[0].Tag != "baseline" || result.Generation != 1 {
			t.Fatal(result)
		}
		exercisePackagedControl(t, client, transport, probeAddress, target.URL)
	}

	docker(t, "stop", "--time", "8", name)
	if strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name)) != "0" {
		t.Fatal("packaged outbound control owner did not shut down cleanly")
	}
	connection, err := net.DialTimeout("tcp", f.native, 100*time.Millisecond)
	if err == nil {
		connection.Close()
		t.Fatal("native Xray API survived Node shutdown")
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
			if event.Message == "started" {
				subscribed = true
			}
		}
		if addresses["HTTP listening"] != "" && addresses["gRPC listening"] != "" && subscribed {
			return addresses
		}
		select {
		case <-ctx.Done():
			t.Fatal("packaged outbound endpoints did not start")
		case <-ticker.C:
		}
	}
}

func exercisePackagedControl(t *testing.T, c clients, transport, address, target string) {
	t.Helper()
	proxy, _ := url.Parse("http://" + address)
	tr := &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	traffic := &http.Client{Transport: tr, Timeout: time.Second}
	content, _ := proto.Marshal(&core.OutboundHandlerConfig{Tag: "managed", ProxySettings: serial.ToTypedMessage(&freedom.Config{})})
	result, code := nativeMutation(t, c, transport, "add", "", content)
	if code != "" || !result.Acknowledged || result.Effect != "completed" {
		t.Fatal(result, code)
	}
	requireTraffic(t, traffic, target, true)
	plan := planReplacement(t, c, transport, "managed", candidate(t, "managed", "blackhole"), "may_change", "retain")
	if plan.Mode != "live_remove_add" || plan.DefaultState != "unobservable" {
		t.Fatal(plan)
	}
	replaced := applyReplacement(t, c, transport, plan.ID)
	if replaced.Outcome != "completed" || len(replaced.Steps) != 2 || !replaced.Steps[0].Acknowledged || !replaced.Steps[1].Acknowledged {
		t.Fatal(replaced)
	}
	requireTraffic(t, traffic, target, false)
	restore := planReplacement(t, c, transport, "managed", content, "may_change", "retain")
	if restored := applyReplacement(t, c, transport, restore.ID); restored.Outcome != "completed" {
		t.Fatal(restored)
	}
	requireTraffic(t, traffic, target, true)
	result, code = nativeMutation(t, c, transport, "remove", "managed", nil)
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
	requireTraffic(t, traffic, target, false)
	result, code = sourceMutation(t, c, transport, "outbound.json", "managed", []byte(`{"outbounds":[{"tag":"managed","protocol":"freedom"}]}`))
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
	requireTraffic(t, traffic, target, true)
	result, code = nativeMutation(t, c, transport, "remove", "managed", nil)
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
}
