//go:build integration && linux

package bootstrap_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/bootstrap"
	"github.com/PapilioVPN/node/internal/modules/instances"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/instances/grpcapi/proto/v1"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

func TestConfigValidationRequiresItsOwnGrantOverMTLS(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	cfg := certificates.Config()
	cfg.Communications.GRPC.Permissions = settings.GRPCPermissions{ReadInstanceStatus: true, ControlInstances: true}
	var server *rpc.Server
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	client := instanceClient(t, server, certificates)
	for _, id := range []string{"unknown", ""} {
		_, err := client.ValidateInstanceConfig(t.Context(), &instancesv1.ValidateInstanceConfigRequest{InstanceId: id})
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("read/control grants must not authorize candidate validation: %v", err)
		}
	}
}

func TestConfigValidationOverMTLSKeepsBothInstancesAndFilesUnchanged(t *testing.T) {
	certificates := testutil.NewCertificates(t)
	first, firstAddress := configuredXray(t, certificates.Directory, "edge-a")
	second, secondAddress := configuredXray(t, certificates.Directory, "edge-b")
	work := t.TempDir()
	first.Validation = &settings.ConfigValidation{WorkDir: work, Timeout: 2 * time.Second, MaxBytes: 1024, MaxConcurrent: 1}
	original, err := os.ReadFile(first.Xray.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	cfg := certificates.Config()
	cfg.Instances = []settings.Instance{first, second}
	// Candidate validation alone must not grant status reads or lifecycle control.
	cfg.Communications.GRPC.Permissions.ValidateInstanceConfig = true
	var server *rpc.Server
	var resources *instances.Resources
	application := bootstrap.New(cfg, "test", fx.NopLogger, fx.Populate(&server, &resources))
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := application.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := resources.Close(); err != nil {
			t.Error(err)
		}
	})
	client := instanceClient(t, server, certificates)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(firstAddress)
	if err != nil {
		t.Fatal(err)
	}
	logPath := first.Xray.ConfigFile + ".candidate-log"
	candidate := fmt.Sprintf(`{"log":{"loglevel":"debug","error":%q,"access":%q},"inbounds":[{"listen":"127.0.0.1","port":%s,"protocol":"http"}],"outbounds":[{"protocol":"freedom"}]}`, first.Xray.ConfigFile, logPath, port)
	for _, test := range []struct {
		content string
		valid   bool
	}{{candidate, true}, {`{"outbounds":[{"protocol":"unknown-private-protocol"}]}`, false}} {
		response, err := client.ValidateInstanceConfig(ctx, &instancesv1.ValidateInstanceConfigRequest{InstanceId: "edge-a", Format: instancesv1.ConfigFormat_CONFIG_FORMAT_JSON, Content: []byte(test.content)})
		if err != nil || response == nil || response.Valid != test.valid || strings.Contains(response.String(), "private") {
			t.Fatalf("native validation over mTLS: %v, %v; want valid=%v", response, err, test.valid)
		}
	}
	for _, test := range []struct {
		id      string
		content []byte
		code    codes.Code
	}{
		{"missing", []byte("{}"), codes.NotFound},
		{"edge-b", []byte("{}"), codes.FailedPrecondition},
		{"edge-a", nil, codes.InvalidArgument},
		{"edge-a", bytes.Repeat([]byte(" "), first.Validation.MaxBytes+1), codes.ResourceExhausted},
	} {
		_, err := client.ValidateInstanceConfig(ctx, &instancesv1.ValidateInstanceConfigRequest{InstanceId: test.id, Format: instancesv1.ConfigFormat_CONFIG_FORMAT_JSON, Content: test.content})
		if status.Code(err) != test.code || strings.Contains(err.Error(), certificates.Directory) {
			t.Fatalf("validation error mapping: %v; want %v", err, test.code)
		}
	}
	if _, err := client.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("validation granted lifecycle control: %v", err)
	}
	if _, err := client.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("validation granted status access: %v", err)
	}
	if err := xrayTraffic(ctx, firstAddress, secondAddress); err != nil {
		t.Fatalf("validation interrupted live traffic: %v", err)
	}
	current, err := os.ReadFile(first.Xray.ConfigFile)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("active configuration was modified: %v", err)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("candidate log was opened: %v", err)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 {
		t.Fatalf("candidate files leaked: %v, %v", entries, err)
	}
}
