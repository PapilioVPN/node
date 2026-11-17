package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/core/engines/instances/grpcapi/proto/v1"
)

func TestStatusAuthenticationAndGrantPrecedeLookup(t *testing.T) {
	for name, item := range map[string]struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		"no peer": {t.Context(), true, codes.Unauthenticated},
		"unverified certificate": {peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{
			State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}},
		}}), true, codes.Unauthenticated},
		"no grant": {verifiedContext(t), false, codes.PermissionDenied},
	} {
		t.Run(name, func(t *testing.T) {
			// A nil reader makes an unintended application call fail immediately.
			handler := grpcapi.Handler{AllowStatusRead: item.grant}
			_, err := handler.GetInstanceStatus(item.ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "unknown"})
			if status.Code(err) != item.code {
				t.Fatalf("authorization mapping = %v", err)
			}
		})
	}
}

func TestStatusRequestValidationAndErrorMapping(t *testing.T) {
	ctx := verifiedContext(t)
	handler := grpcapi.Handler{AllowStatusRead: true}
	for _, request := range []*instancesv1.GetInstanceStatusRequest{nil, {}, {InstanceId: "  "}} {
		if _, err := handler.GetInstanceStatus(ctx, request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid identity reached reader: %v", err)
		}
	}
	for cause, expected := range map[error]codes.Code{
		fmt.Errorf("wrapped: %w", domain.ErrNotFound):                   codes.NotFound,
		fmt.Errorf("private native path: %w", context.Canceled):         codes.Canceled,
		fmt.Errorf("private native path: %w", context.DeadlineExceeded): codes.DeadlineExceeded,
		errors.New("private implementation path"):                       codes.Internal,
	} {
		handler.Status = statusReader(func(received context.Context, id domain.InstanceID) (domain.Status, error) {
			if received != ctx || id != "edge-a" {
				t.Fatal("identity or context changed at transport boundary")
			}
			return domain.Status{}, cause
		})
		_, err := handler.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"})
		if status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("read error mapping = %v", err)
		}
	}
}

func TestExecutionFailureIsAReadableOutcome(t *testing.T) {
	handler := grpcapi.Handler{AllowStatusRead: true, Status: statusReader(func(context.Context, domain.InstanceID) (domain.Status, error) {
		return domain.Status{Instance: domain.Instance{ID: "edge-a", Kind: domain.Xray}, State: domain.Exited, Failure: context.Canceled}, nil
	})}
	response, err := handler.GetInstanceStatus(verifiedContext(t), &instancesv1.GetInstanceStatusRequest{InstanceId: "edge-a"})
	if err != nil || response.GetState() != instancesv1.InstanceState_INSTANCE_STATE_EXITED || response.Failure == nil {
		t.Fatalf("execution failure was confused with failed status read: %v, %v", response, err)
	}
}

// Controlled native peer context for unit tests, not mTLS handshake evidence.
func verifiedContext(t *testing.T) context.Context {
	t.Helper()
	return peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}},
	}})
}

type statusReader func(context.Context, domain.InstanceID) (domain.Status, error)

func (f statusReader) GetInstanceStatus(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	return f(ctx, id)
}
