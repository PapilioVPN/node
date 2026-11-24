package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configsv1 "github.com/PapilioVPN/node/internal/engines/xray/configs/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"golang.org/x/sys/unix"
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

	assertStillServing(t, f, ctx, before[0].Name())
}

func assertStillServing(t *testing.T, f fixture, ctx context.Context, directory string) {
	t.Helper()
	after, err := os.ReadDir(f.config.RuntimeDirectory)
	require(t, err)
	if len(after) != 1 || after[0].Name() != directory {
		t.Fatalf("runtime directories = %v, want only %s", after, directory)
	}
	assertNativeSocket(t, f, ctx, directory)
}

func assertNativeSocket(t *testing.T, f fixture, ctx context.Context, directory string) {
	t.Helper()
	socket := url.URL{Scheme: "unix", Path: filepath.Join(f.config.RuntimeDirectory, directory, "api.sock")}
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
	result, err := f.configs.ValidateConfig(t.Context(), proposedConfig("{}"))
	require(t, err)
	if !result.GetValid() || !postValidationBody(t, f, t.Context(), strings.NewReader("{}")).Valid {
		t.Fatal("candidate validation depended on the configured file")
	}
	require(t, os.Remove(f.config.RuntimeDirectory))
	_, err = f.configs.ValidateConfig(t.Context(), proposedConfig("{}"))
	if status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), f.config.RuntimeDirectory) {
		t.Fatalf("candidate preparation failure = %v, want sanitized Unavailable", err)
	}
}

func TestValidateProposedConfigWhileXrayIsServing(t *testing.T) {
	const baseline = `{"log":{"loglevel":"none"}}`
	f := newFixture(t, baseline)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := f.instances.Start(ctx, &instancesv1.XrayInstanceRequest{Id: instanceID})
	require(t, err)
	active, err := os.ReadDir(f.config.RuntimeDirectory)
	require(t, err)
	if len(active) != 1 {
		t.Fatalf("runtime directories = %v, want exactly one", active)
	}
	logPath := filepath.Join(filepath.Dir(f.config.ConfigFile), "dry-run.log")
	valid := fmt.Sprintf(`{"log":{"error":%q,"loglevel":"debug"},"outbounds":[{"protocol":"freedom"}]}`, logPath)
	for _, tc := range []struct {
		name, source, httpCode string
		code                   configsv1.XrayConfigIssueCode
	}{
		{name: "valid", source: valid},
		{name: "semantic", source: `{"outbounds":[{"protocol":"sample-secret"}]}`, httpCode: "config_rejected", code: configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_REJECTED},
		{name: "syntax", source: `{"private":}`, httpCode: "config_decode_error", code: configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR},
		{name: "whitespace", source: " \n", httpCode: "config_decode_error", code: configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR},
		{name: "null", source: "null", httpCode: "config_decode_error", code: configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantValid := tc.httpCode == ""
			got, err := f.configs.ValidateConfig(ctx, proposedConfig(tc.source))
			require(t, err)
			if got.GetId() != instanceID || got.GetValid() != wantValid || got.GetIssue().GetCode() != tc.code ||
				(got.GetIssue() == nil) != wantValid || strings.Contains(got.String(), "sample-secret") {
				t.Fatalf("gRPC validation = %v", got)
			}
			res := postValidationBody(t, f, ctx, strings.NewReader(tc.source))
			if res.ID != instanceID || res.Valid != wantValid || (res.Issue == nil) != wantValid ||
				res.Issue != nil && res.Issue.Code != tc.httpCode {
				t.Fatalf("HTTP validation = %+v, issue = %+v", res, res.Issue)
			}
			assertStillServing(t, f, ctx, active[0].Name())
		})
	}
	res, err := f.configs.ValidateConfig(ctx, proposedConfig(""))
	require(t, err)
	if res.GetValid() || res.GetIssue().GetCode() != configsv1.XrayConfigIssueCode_XRAY_CONFIG_ISSUE_CODE_CONFIG_DECODE_ERROR {
		t.Fatalf("explicit empty candidate = %v", res)
	}
	// A chunked request can explicitly send a body stream of zero bytes.
	if res := postValidationBody(t, f, ctx, io.LimitReader(strings.NewReader(""), 0)); res.Valid ||
		res.Issue == nil || res.Issue.Code != "config_decode_error" {
		t.Fatalf("explicit empty HTTP stream = %+v", res)
	}
	res, err = f.configs.ValidateConfig(ctx, &configsv1.ValidateXrayConfigRequest{Id: instanceID})
	require(t, err)
	if !res.GetValid() || !postValidation(t, f, ctx).Valid {
		t.Fatal("omitting the candidate did not validate the configured file")
	}
	if _, err := os.Stat(logPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate log destination was touched: %v", err)
	}
	data, err := os.ReadFile(f.config.ConfigFile)
	require(t, err)
	if string(data) != baseline {
		t.Fatal("candidate changed the configured file")
	}
}

func TestProposedConfigTimeoutAndAdmission(t *testing.T) {
	f := newFixture(t, `{"log":{"loglevel":"none"}}`)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	_, err := f.instances.Start(ctx, &instancesv1.XrayInstanceRequest{Id: instanceID})
	require(t, err)
	active, err := os.ReadDir(f.config.RuntimeDirectory)
	require(t, err)
	if len(active) != 1 {
		t.Fatalf("runtime directories = %v, want exactly one", active)
	}
	// Real Xray reads this certificate during configuration loading.
	certificate := filepath.Join(filepath.Dir(f.config.ConfigFile), "blocked-cert.pem")
	require(t, unix.Mkfifo(certificate, 0600))
	candidate := fmt.Sprintf(`{"inbounds":[{"listen":"127.0.0.1","port":12345,"protocol":"vless","settings":{"decryption":"none"},"streamSettings":{"security":"tls","tlsSettings":{"certificates":[{"certificateFile":%q}]}}}]}`, certificate)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+route, strings.NewReader(candidate))
	require(t, err)
	done := make(chan error, 1)
	go func() {
		resp, err := f.http.Client().Do(req)
		if err != nil {
			done <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusGatewayTimeout {
			done <- fmt.Errorf("validation status = %s, want 504", resp.Status)
			return
		}
		done <- nil
	}()
	awaitValidation(t, f, ctx, done)
	assertNativeSocket(t, f, ctx, active[0].Name())
	_, err = f.configs.ValidateConfig(ctx, &configsv1.ValidateXrayConfigRequest{Id: instanceID})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("concurrent legacy validation = %v, want Aborted", err)
	}
	select {
	case err := <-done:
		require(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	assertStillServing(t, f, ctx, active[0].Name())

	rpcCtx, cancelRPC := context.WithCancel(ctx)
	defer cancelRPC()
	go func() {
		_, err := f.configs.ValidateConfig(rpcCtx, proposedConfig(candidate))
		done <- err
	}()
	awaitValidation(t, f, ctx, done)
	cancelRPC()
	if err := <-done; status.Code(err) != codes.Canceled {
		t.Fatalf("canceled gRPC validation = %v, want Canceled", err)
	}
	for {
		retry, err := f.configs.ValidateConfig(ctx, proposedConfig("{}"))
		if status.Code(err) != codes.Aborted {
			require(t, err)
			if !retry.GetValid() {
				t.Fatalf("validation after cancellation = %v", retry)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	assertStillServing(t, f, ctx, active[0].Name())
}

func awaitValidation(t *testing.T, f fixture, ctx context.Context, done <-chan error) {
	t.Helper()
	for {
		dirs, err := os.ReadDir(f.config.RuntimeDirectory)
		require(t, err)
		if len(dirs) == 2 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("validation ended before native admission: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestProposedConfigSizeLimit(t *testing.T) {
	f := newFixture(t, "{}")
	source := "{" + strings.Repeat(" ", runconfig.MaxSourceBytes-2) + "}"
	if !postValidationBody(t, f, t.Context(), strings.NewReader(source)).Valid {
		t.Fatal("candidate at the Start limit was rejected")
	}
	result, err := f.configs.ValidateConfig(t.Context(), proposedConfig(source))
	require(t, err)
	if !result.GetValid() {
		t.Fatal("gRPC candidate at the Start limit was rejected")
	}
	source += " "
	_, err = f.configs.ValidateConfig(t.Context(), proposedConfig(source))
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("oversized candidate = %v, want ResourceExhausted", err)
	}
	resp, err := f.http.Client().Post(f.http.URL+route, "application/json", strings.NewReader(source))
	require(t, err)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized candidate HTTP status = %s, want 413", resp.Status)
	}
}

func proposedConfig(source string) *configsv1.ValidateXrayConfigRequest {
	return &configsv1.ValidateXrayConfigRequest{Id: instanceID, ConfigJson: []byte(source)}
}

func postValidation(t testing.TB, f fixture, ctx context.Context) httpResult {
	t.Helper()
	return postValidationBody(t, f, ctx, nil)
}

func postValidationBody(t testing.TB, f fixture, ctx context.Context, body io.Reader) httpResult {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.http.URL+route, body)
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
