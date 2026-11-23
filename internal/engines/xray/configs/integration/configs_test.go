package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

const route = "/v1/xray/instances/primary/config-validations"

type httpResult struct {
	ID    string
	Valid bool
	Issue *struct {
		Code         string
		Line, Column uint32
	}
}

func TestValidateConfiguredFileWhileXrayIsServing(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	instance := &instancesv1.XrayInstanceRequest{Id: instanceID}
	running, err := f.instances.Start(ctx, instance)
	require(t, err)
	if running.GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY {
		t.Fatalf("start = %v", running)
	}
	before, err := os.ReadDir(f.config.RuntimeDirectory)
	require(t, err)
	if len(before) != 1 {
		t.Fatalf("private runtime directories = %v, want only the active instance", before)
	}

	request := &configsv1.ValidateXrayConfigRequest{Id: instanceID}
	rpcResult, err := f.configs.ValidateConfig(ctx, request)
	require(t, err)
	if rpcResult.GetId() != instanceID || !rpcResult.GetValid() || rpcResult.GetIssue() != nil {
		t.Fatalf("valid gRPC result = %v", rpcResult)
	}
	if res := postValidation(t, f, ctx); res.ID != instanceID || !res.Valid || res.Issue != nil {
		t.Fatalf("valid HTTP result = %+v", res)
	}

	require(t, os.WriteFile(f.config.ConfigFile, []byte("{\n\"sample-secret\":,\n}"), 0600))
	rpcResult, err = f.configs.ValidateConfig(ctx, request)
	require(t, err)
	issue := rpcResult.GetIssue()
	if rpcResult.GetValid() || issue.GetCode() != configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR ||
		issue.GetLine() != 2 || issue.GetColumn() == 0 || strings.Contains(rpcResult.String(), "sample-secret") {
		t.Fatalf("invalid gRPC result = %v", rpcResult)
	}
	res := postValidation(t, f, ctx)
	if res.Valid || res.Issue == nil || res.Issue.Code != "config_decode_error" || res.Issue.Line != 2 || res.Issue.Column == 0 {
		t.Fatalf("invalid HTTP result = %+v, issue = %+v", res, res.Issue)
	}

	require(t, os.WriteFile(f.config.ConfigFile, []byte(`{"outbounds":[{"protocol":"sample-secret"}]}`), 0600))
	rpcResult, err = f.configs.ValidateConfig(ctx, request)
	require(t, err)
	if rpcResult.GetValid() || rpcResult.GetIssue().GetCode() != configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_REJECTED {
		t.Fatalf("semantic rejection = %v", rpcResult)
	}

	after, err := os.ReadDir(f.config.RuntimeDirectory)
	require(t, err)
	if len(after) != 1 || after[0].Name() != before[0].Name() {
		t.Fatalf("active runtime changed from %v to %v", before, after)
	}
	socket := url.URL{Scheme: "unix", Path: filepath.Join(f.config.RuntimeDirectory, before[0].Name(), "api.sock")}
	conn, err := grpc.NewClient(socket.String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require(t, err)
	defer conn.Close()
	require(t, conn.Invoke(ctx, "/xray.app.stats.command.StatsService/GetSysStats", &emptypb.Empty{}, &emptypb.Empty{}))
}

func TestMissingConfiguredFileFailsValidation(t *testing.T) {
	f := newFixture(t, "{}")
	require(t, os.Remove(f.config.ConfigFile))
	_, err := f.configs.ValidateConfig(t.Context(), &configsv1.ValidateXrayConfigRequest{Id: instanceID})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("missing file validation = %v, want Unavailable", err)
	}
	resp, err := f.http.Client().Post(f.http.URL+route, "", nil)
	require(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing file HTTP status = %v", resp.StatusCode)
	}
}

func postValidation(t testing.TB, f fixture, ctx context.Context) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+route, nil)
	require(t, err)
	resp, err := f.http.Client().Do(req)
	require(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validation HTTP status = %v", resp.StatusCode)
	}
	var result httpResult
	require(t, json.NewDecoder(resp.Body).Decode(&result))
	return result
}
