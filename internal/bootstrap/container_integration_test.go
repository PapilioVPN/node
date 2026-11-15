//go:build container

package bootstrap_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
	nodev1 "github.com/PapilioVPN/node/internal/modules/instances/grpcapi/proto/v1"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

var image = flag.String("image", "", "prebuilt image to verify")

// TestContainer executes the packaged binary with real mTLS and SIGTERM.
func TestContainer(t *testing.T) {
	if *image == "" {
		t.Fatal("-image must identify the already built runtime image")
	}
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Listen = "0.0.0.0:9443"
	cfg.Communications.GRPC.TLS.CertificateFile = "server.pem"
	cfg.Communications.GRPC.TLS.PrivateKeyFile = "server-key.pem"
	cfg.Communications.GRPC.TLS.ClientCAFile = "ca.pem"
	cfg.Communications.GRPC.Permissions.ReadInstanceStatus = true
	cfg.Communications.GRPC.Permissions.ControlInstances = true
	cfg.Communications.GRPC.Permissions.ValidateInstanceConfig = true
	cfg.Communications.GRPC.Permissions.ReadEngineInfo = true
	cfg.Communications.GRPC.Permissions.ReadEngineStats = true
	contents, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certificates.Directory, "config.yml"), contents, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", "run", "--detach", "--read-only",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--publish", "127.0.0.1::9443", "--mount", "type=bind,src="+certificates.Directory+",dst=/app,readonly", *image).Output()
	if err != nil {
		t.Fatalf("start isolated runtime: %v", err)
	}
	containerID := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if output, err := exec.CommandContext(cleanup, "docker", "rm", "--force", containerID).CombinedOutput(); err != nil {
			t.Errorf("clean owned container: %s, %v", output, err)
		}
	})
	output, err = exec.CommandContext(ctx, "docker", "port", containerID, "9443/tcp").Output()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(strings.TrimSpace(string(output)), grpc.WithTransportCredentials(credentials.NewTLS(certificates.ClientTLS(t))))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	request, stopRequest := context.WithTimeout(ctx, 10*time.Second)
	defer stopRequest()
	info, err := systemv1.NewNodeServiceClient(connection).GetNodeInfo(request, &systemv1.GetNodeInfoRequest{}, grpc.WaitForReady(true))
	if err != nil || info.GetNodeId() != "test-node" || info.GetVersion() == "" {
		logs, _ := exec.CommandContext(ctx, "docker", "logs", containerID).CombinedOutput()
		t.Fatalf("packaged operation failed: %v, %v; logs: %s", info, err, logs)
	}
	// The packaged binary must honor independent grants loaded from its YAML.
	_, err = nodev1.NewInstanceServiceClient(connection).GetInstanceStatus(request, &nodev1.GetInstanceStatusRequest{InstanceId: "unconfigured"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("packaged status grant/service failed: %v", err)
	}
	page, err := nodev1.NewInstanceServiceClient(connection).ListInstanceStatuses(request, &nodev1.ListInstanceStatusesRequest{PageSize: 1})
	if err != nil || len(page.GetItems()) != 0 || page.NextAfterInstanceId != "" {
		t.Fatalf("packaged collection grant/service failed: %v, %v", page, err)
	}
	if _, err := nodev1.NewInstanceServiceClient(connection).StartInstance(request, &nodev1.StartInstanceRequest{InstanceId: "unconfigured"}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged start grant/service failed: %v", err)
	}
	if _, err := nodev1.NewInstanceServiceClient(connection).StopInstance(request, &nodev1.StopInstanceRequest{InstanceId: "unconfigured"}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged stop grant/service failed: %v", err)
	}
	if _, err := nodev1.NewInstanceServiceClient(connection).ValidateInstanceConfig(request, &nodev1.ValidateInstanceConfigRequest{InstanceId: "unconfigured", Format: nodev1.ConfigFormat_CONFIG_FORMAT_JSON, Content: []byte("{}")}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged validation grant/service failed: %v", err)
	}
	if _, err := xrayv1.NewXrayServiceClient(connection).GetBinaryInfo(request, &xrayv1.GetBinaryInfoRequest{InstanceId: "unconfigured"}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged Xray information grant/service failed: %v", err)
	}
	if _, err := xrayv1.NewXrayServiceClient(connection).GetRuntimeInfo(request, &xrayv1.GetRuntimeInfoRequest{InstanceId: "unconfigured"}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged Xray runtime grant/service failed: %v", err)
	}
	if _, err := xrayv1.NewXrayServiceClient(connection).GetTrafficCounter(request, &xrayv1.GetTrafficCounterRequest{
		InstanceId: "unconfigured", Scope: xrayv1.TrafficScope_TRAFFIC_SCOPE_INBOUND,
		Name: "proxy", Direction: xrayv1.TrafficDirection_TRAFFIC_DIRECTION_UPLINK,
	}); status.Code(err) != codes.NotFound {
		t.Fatalf("packaged Xray traffic grant/service failed: %v", err)
	}
	if output, err := exec.CommandContext(ctx, "docker", "stop", "--time", "5", containerID).CombinedOutput(); err != nil {
		t.Fatalf("stop runtime: %s, %v", output, err)
	}
	output, err = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.ExitCode}}", containerID).Output()
	if err != nil || strings.TrimSpace(string(output)) != "0" {
		t.Fatalf("SIGTERM did not exit cleanly: %s, %v", output, err)
	}
}
