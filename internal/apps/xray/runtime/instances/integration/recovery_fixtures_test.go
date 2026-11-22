//go:build integration && linux

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/prometheus/procfs"
	"go.uber.org/fx"
	"go.yaml.in/yaml/v3"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func privateDirectory(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	if err := os.Chmod(p, 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func recoverableEngine(t *testing.T, id string) engine {
	e := engineFixture(t, id)
	e.settings.Xray.StateDirectory = privateDirectory(t)
	e.settings.Xray.WorkDirectory = privateDirectory(t)
	t.Cleanup(func() {
		data, err := os.ReadFile(filepath.Join(e.settings.Xray.StateDirectory, "instance-execution.json"))
		if err != nil {
			return
		}
		var r struct {
			PID        int
			StartTicks uint64
		}
		if json.Unmarshal(data, &r) == nil && r.PID > 0 && r.StartTicks > 0 {
			fd, err := unix.PidfdOpen(r.PID, 0)
			if err == nil {
				fs, fsErr := procfs.NewFS("/proc")
				if fsErr == nil {
					if process, err := fs.Proc(r.PID); err == nil {
						if stat, err := process.Stat(); err == nil && stat.Starttime == r.StartTicks {
							unix.PidfdSendSignal(fd, unix.SIGKILL, nil, 0)
						}
					}
				}
				unix.Close(fd)
			}
		}
	})
	return e
}
func buildNode(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-p=2", "-o", p, "github.com/PapilioVPN/node/cmd/node").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	return p
}
func availableAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a := l.Addr().String()
	l.Close()
	return a
}

type nodeProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	log     string
}

func (p *nodeProcess) stop(t *testing.T, signal syscall.Signal) {
	t.Helper()
	select {
	case <-p.done:
		return
	default:
	}
	if err := p.command.Process.Signal(signal); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.done:
	case <-time.After(35 * time.Second):
		p.command.Process.Kill()
		<-p.done
		t.Fatal("Node did not join")
	}
	if signal == syscall.SIGTERM && p.err != nil {
		data := nodeErrors(p.log)
		t.Fatalf("Node shutdown: %v %s", p.err, data)
	}
}
func startExecutable(t *testing.T, binary string, pki testutil.PKI, config settings.Config) (*nodeProcess, clients) {
	t.Helper()
	dir := t.TempDir()
	data, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "console")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-config", path)
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		f.Close()
		t.Fatal(err)
	}
	p := &nodeProcess{command: cmd, done: make(chan struct{}), log: logPath}
	go func() { p.err = cmd.Wait(); f.Close(); close(p.done) }()
	t.Cleanup(func() { p.stop(t, syscall.SIGTERM) })
	c := connectClients(t, pki.Client, config, map[string]string{"HTTP listening": config.Communications.HTTP.Address, "gRPC listening": config.Communications.GRPC.Address})
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		startupLog, _ := os.ReadFile(logPath)
		allReady := bytes.Contains(startupLog, []byte(`"msg":"started"`))
		for _, transport := range []string{"http", "grpc", "nats"} {
			_, err := c.status(ctx, transport, config.Instances[0].ID)
			if err != nil {
				allReady = false
				break
			}
		}
		cancel()
		if allReady {
			return p, c
		}
		select {
		case <-deadline.C:
			data := nodeErrors(logPath)
			t.Fatalf("Node did not start: %s", data)
		case <-p.done:
			data := nodeErrors(logPath)
			t.Fatalf("Node exited: %v %s", p.err, data)
		case <-tick.C:
		}
	}
}
func childRecord(t *testing.T, e engine) struct {
	PID             int
	ExecutionID     string
	Generation      uint64
	Token           string
	SourceDirectory string
} {
	t.Helper()
	var r struct {
		PID             int
		ExecutionID     string
		Generation      uint64
		Token           string
		SourceDirectory string
	}
	data, err := os.ReadFile(filepath.Join(e.settings.Xray.StateDirectory, "instance-execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	return r
}
func (c clients) extended(ctx context.Context, transport, operation, id string, q map[string]any) (map[string]any, error) {
	if q == nil {
		q = map[string]any{}
	}
	var data []byte
	var err error
	if transport == "grpc" {
		client := v1.NewInstancesServiceClient(c.grpc)
		var message proto.Message
		str := func(k string) string { v, _ := q[k].(string); return v }
		num := func(k string) uint32 { v, _ := q[k].(uint32); return v }
		flag := func(k string) bool { v, _ := q[k].(bool); return v }
		revision, _ := strconv.ParseUint(str("expected_revision"), 10, 64)
		switch operation {
		case "list":
			message, err = client.ListInstances(ctx, &v1.ListInstancesRequest{After: str("after"), Limit: num("limit")})
		case "repair":
			message, err = client.RepairInstance(ctx, &v1.RepairInstanceRequest{InstanceId: id, ExecutionId: str("execution_id"), MaxAttempts: num("max_attempts"), ReplaceUnready: flag("replace_unready")})
		case "retention":
			message, err = client.SetInstanceRetention(ctx, &v1.SetInstanceRetentionRequest{InstanceId: id, ExecutionId: str("execution_id"), Preserve: flag("preserve")})
		case "lease":
			message, err = client.GetInstanceLease(ctx, &v1.GetInstanceLeaseRequest{InstanceId: id})
		case "acquire":
			message, err = client.AcquireInstanceLease(ctx, &v1.AcquireInstanceLeaseRequest{InstanceId: id, ExpectedRevision: revision, ExecutionId: str("execution_id"), Owner: str("owner"), TtlSeconds: num("ttl_seconds"), Policy: str("policy")})
		case "renew":
			message, err = client.RenewInstanceLease(ctx, &v1.RenewInstanceLeaseRequest{InstanceId: id, ExpectedRevision: revision, Token: str("token"), TtlSeconds: num("ttl_seconds")})
		case "release":
			message, err = client.ReleaseInstanceLease(ctx, &v1.ReleaseInstanceLeaseRequest{InstanceId: id, ExpectedRevision: revision, Token: str("token")})
		}
		if err != nil {
			for _, detail := range status.Convert(err).Details() {
				if r, ok := detail.(*v1.RepairResult); ok {
					message = r
					break
				}
			}
			if message == nil || !message.ProtoReflect().IsValid() {
				return nil, err
			}
		}
		data, _ = (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
	} else if transport == "http" {
		path, method := root+id, "GET"
		switch operation {
		case "list":
			path = "/v1/core/engines/instances"
		case "repair":
			path += "/repairs"
			method = "POST"
		case "retention":
			path += "/retention"
			method = "PUT"
		case "lease":
			path += "/lease"
		case "acquire":
			path += "/leases"
			method = "POST"
		case "renew":
			path += "/lease"
			method = "PUT"
		case "release":
			path += "/lease"
			method = "DELETE"
		}
		var body io.Reader
		if method != "GET" {
			encoded, _ := json.Marshal(q)
			body = bytes.NewReader(encoded)
		}
		request, _ := http.NewRequestWithContext(ctx, method, c.address+path, body)
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := c.http.Do(request)
		if requestErr != nil {
			return nil, requestErr
		}
		defer response.Body.Close()
		data, err = io.ReadAll(response.Body)
		if response.StatusCode != 200 {
			err = fmt.Errorf("HTTP %d", response.StatusCode)
		}
	} else {
		suffix := map[string]string{"list": "list.v1.get", "repair": "v1.repair", "retention": "retention.v1.set", "lease": "lease.v1.get", "acquire": "lease.v1.acquire", "renew": "lease.v1.renew", "release": "lease.v1.release"}[operation]
		if operation != "list" {
			q["instance_id"] = id
		}
		encoded, _ := json.Marshal(q)
		reply, requestErr := c.nats.RequestWithContext(ctx, prefix+".core.engines.instances."+suffix, encoded)
		if requestErr != nil {
			return nil, requestErr
		}
		data = reply.Data
	}
	var out map[string]any
	if decodeErr := json.Unmarshal(data, &out); decodeErr != nil {
		return nil, decodeErr
	}
	if code, ok := out["error"].(string); ok && code != "" {
		err = errors.New("instance operation failed")
	}
	return out, err
}
func requireExtended(t *testing.T, c clients, transport, op, id string, q map[string]any) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := c.extended(ctx, transport, op, id, q)
	if err != nil {
		t.Fatalf("%s %s: %v %+v", transport, op, err, r)
	}
	return r
}

func mustJSON(v any) []byte { data, _ := json.Marshal(v); return data }

func applyFixtureSources(t *testing.T, c clients, e engine) {
	t.Helper()
	for _, op := range []string{"replace", "apply"} {
		q := map[string]any{"expected_revision": "1"}
		path := "/v1/xray/runtime/instances/" + e.settings.ID + "/configuration/applications"
		method := "POST"
		if op == "replace" {
			q = map[string]any{"expected_revision": "0", "sources": []any{map[string]any{"name": "base.json", "content": e.original}}}
			path = "/v1/xray/runtime/instances/" + e.settings.ID + "/configuration"
			method = "PUT"
		}
		req, _ := http.NewRequest(method, c.address+path, bytes.NewReader(mustJSON(q)))
		req.Header.Set("Content-Type", "application/json")
		response, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 {
			t.Fatalf("desired source %s: %s", op, data)
		}
	}
	awaitReady(t, c, "http", e.settings.ID)
}

func nodeErrors(path string) []byte {
	data, _ := os.ReadFile(path)
	var out []byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if bytes.Contains(line, []byte(`"level":"ERROR"`)) {
			out = append(out, line...)
			out = append(out, '\n')
		}
	}
	if len(out) > 6000 {
		out = out[len(out)-6000:]
	}
	return out
}

func startObservedNode(t *testing.T, pki testutil.PKI, config settings.Config) (*fx.App, clients, []domain.OutputBinding) {
	t.Helper()
	logger := &listenerLog{Handler: slog.NewTextHandler(io.Discard, nil), addresses: map[string]string{}}
	var outputs []domain.OutputBinding
	app := bootstrap.New(config, slog.New(logger), fx.Invoke(func(in outputConsumer) { outputs = in.Sources }))
	ctx, cancel := context.WithTimeout(context.Background(), config.StartupTimeout)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return app, connectClients(t, pki.Client, config, logger.addresses), outputs
}

func awaitExpiredLease(t *testing.T, c clients, transport, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		r := requireExtended(t, c, transport, "lease", id, nil)
		if r["state"] == "expired" {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatal("lease did not expire", r)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

type outputConsumer struct {
	fx.In
	Sources []domain.OutputBinding `group:"xray-instance-output"`
}

func injectPendingIntent(t *testing.T, e engine) {
	t.Helper()
	path := filepath.Join(e.settings.Xray.StateDirectory, "instance-execution.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]json.RawMessage
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	r["PID"] = json.RawMessage("0")
	r["StartTicks"] = json.RawMessage("0")
	r["Pending"] = json.RawMessage("true")
	if err := os.WriteFile(path, mustJSON(r), 0600); err != nil {
		t.Fatal(err)
	}
}

func retainedTunnel(t *testing.T, proxy, target string) (func(), func()) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", proxy, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	connection.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintf(connection, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)
	response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		connection.Close()
		t.Fatal("native tunnel", err)
	}
	verify := func() {
		t.Helper()
		connection.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive\r\n\r\n", target)
		response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
		if err != nil {
			t.Fatal("retained traffic connection", err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(data) != "instance-traffic-proof" {
			t.Fatal("retained traffic body", err)
		}
	}
	return verify, func() { connection.Close() }
}
