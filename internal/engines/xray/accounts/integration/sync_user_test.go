package integration_test

import (
	"context"
	"errors"
	"sync/atomic"

	"encoding/json"
	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const rotatedCredential = "4310b7cc-e6d2-4101-ac03-8ec2a553b835"

func TestSyncUserHTTP(t *testing.T) {
	f := startFixture(t)
	targetPort := trafficTarget(t)
	syncUser := func(uuid *string, added, removed int) {
		t.Helper()
		input := map[string]any{"email": strings.ToLower(email)}
		if uuid != nil {
			input["uuid"] = *uuid
		}
		raw := f.request(t, http.MethodPut, accountsPath+"/membership", input, http.StatusOK)
		var result struct {
			Outcome        string
			Added, Removed int
		}
		require(t, json.Unmarshal(raw, &result))
		if result.Outcome != "confirmed" || result.Added != added || result.Removed != removed {
			t.Fatalf("unexpected sync result: %+v", result)
		}
	}
	syncUser(ptr(credential), 1, 0)
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, targetPort, true)
	syncUser(ptr(collision), 0, 0)
	syncUser(ptr(rotatedCredential), 1, 1)
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, targetPort, false)
	checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, true)
	syncUser(nil, 0, 1)
	f.checkCount(t, 0)
	checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, false)
	syncUser(nil, 0, 0)
}

func ptr(text string) *string { return &text }

func trafficTarget(t *testing.T) int {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "vless fixture")
	}))
	t.Cleanup(target.Close)
	return target.Listener.Addr().(*net.TCPAddr).Port
}

func (f fixture) checkCount(t *testing.T, want int64) {
	t.Helper()
	raw := f.request(t, http.MethodGet, accountsPath+"/count", nil, http.StatusOK)
	var result struct{ Count int64 }
	require(t, json.Unmarshal(raw, &result))
	if result.Count != want {
		t.Fatalf("count = %d, want %d", result.Count, want)
	}
}

func TestSyncUserGRPC(t *testing.T) {
	f := startFixture(t)
	targetPort := trafficTarget(t)
	input := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: email}
	syncUser := func(uuid *string, added, removed uint32) {
		t.Helper()
		input.Uuid = uuid
		result, err := f.grpc.SyncUser(t.Context(), input)
		require(t, err)
		if result.GetOutcome() != "confirmed" || result.GetAdded() != added || result.GetRemoved() != removed {
			t.Fatal("unexpected sync outcome or effects")
		}
	}
	syncUser(ptr(credential), 1, 0)
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, targetPort, true)
	syncUser(ptr(rotatedCredential), 1, 1)
	checkTraffic(t, f.inboundPort, targetPort, false)
	checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, true)
	syncUser(nil, 0, 1)
	f.checkCount(t, 0)
	checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, false)
}

type testExecution struct{ conn *grpc.ClientConn }

func (e testExecution) NativeConnection(string) (*grpc.ClientConn, error) { return e.conn, nil }

func TestSyncUserInterruptedRotation(t *testing.T) {
	for _, tc := range []struct {
		name, failure string
		outcome       membership.Outcome
	}{
		{"canceled-between-effects", "canceled", membership.Partial},
		{"lost-native-acknowledgement", "native_acknowledgement_unknown", membership.Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := startFixture(t)
			targetPort := trafficTarget(t)
			direct, err := f.instance.NativeConnection(instanceID)
			require(t, err)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var writes atomic.Int32
			conn, err := grpc.NewClient(direct.Target(), grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, conn *grpc.ClientConn, call grpc.UnaryInvoker, opts ...grpc.CallOption) error {
					err := call(ctx, method, req, reply, conn, opts...)
					if err == nil && method == native.AlterInboundMethod {
						n := writes.Add(1)
						if n == 2 && tc.outcome == membership.Partial {
							cancel()
						}
						if n == 3 && tc.outcome == membership.Unknown {
							return errors.New(rotatedCredential)
						}
					}
					return err
				}))
			require(t, err)
			defer conn.Close()
			service := membership.New(native.New(testExecution{conn}))
			request := membership.SyncUserRequest{Scope: membership.Scope{InstanceID: instanceID, InboundTag: "vless"}, Email: email, UUID: ptr(credential)}
			first, err := service.SyncUser(t.Context(), request)
			require(t, err)
			if first.Outcome != membership.Confirmed || first.Added != 1 {
				t.Fatal("fixture account did not apply")
			}
			request.UUID = ptr(rotatedCredential)
			result, err := service.SyncUser(ctx, request)
			require(t, err)
			if result.Outcome != tc.outcome || result.Removed != 1 || result.Added != 0 || result.Failure != tc.failure {
				t.Fatal("rotation failed outcome or redaction check")
			}
			checkTraffic(t, f.inboundPort, targetPort, false)
			checkTrafficCredential(t, f.inboundPort, targetPort, rotatedCredential, tc.outcome == membership.Unknown)
			if tc.outcome == membership.Unknown {
				f.checkCount(t, 1)
				retry, err := service.SyncUser(t.Context(), request)
				require(t, err)
				if retry.Outcome != membership.NotApplied || retry.Failure != "fenced" {
					t.Fatal("uncertain execution accepted another mutation")
				}
			} else {
				f.checkCount(t, 0)
			}
		})
	}
}
