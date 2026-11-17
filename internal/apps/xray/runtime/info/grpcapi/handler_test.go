package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi/proto/v1"
)

type binaryReader func(context.Context, instances.InstanceID) (domain.InstanceBinaryInfo, error)

func (f binaryReader) GetBinaryInfo(ctx context.Context, id instances.InstanceID) (domain.InstanceBinaryInfo, error) {
	return f(ctx, id)
}

// A controlled peer is unit evidence only; bootstrap tests perform native mTLS.
func verifiedContext(t *testing.T) context.Context {
	t.Helper()
	return peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}},
	}})
}

func TestBinaryInfoAuthenticationAndGrantPrecedeRequestAndLookup(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated},
		{peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{{}}}}}), true, codes.Unauthenticated},
		{verifiedContext(t), false, codes.PermissionDenied},
	} {
		// Nil Info fails immediately if authorization reaches the application.
		handler := grpcapi.InfoAPI{AllowInfoRead: test.grant}
		if response, err := handler.GetBinaryInfo(test.ctx, nil); response != nil || status.Code(err) != test.code {
			t.Fatalf("authorization order: %v, %v; want %v", response, err, test.code)
		}
	}
	handler := grpcapi.InfoAPI{AllowInfoRead: true}
	for _, request := range []*xrayv1.GetBinaryInfoRequest{nil, {}, {InstanceId: " "}} {
		if _, err := handler.GetBinaryInfo(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid input reached application: %v", err)
		}
	}
}

func TestBinaryInfoRetainsIdentityAndNativeFacts(t *testing.T) {
	ctx := verifiedContext(t)
	observed := time.UnixMilli(1700000000123).UTC()
	handler := grpcapi.InfoAPI{AllowInfoRead: true, Info: binaryReader(func(received context.Context, id instances.InstanceID) (domain.InstanceBinaryInfo, error) {
		if received != ctx || id != " edge " {
			t.Fatal("context or exact identity changed")
		}
		return domain.InstanceBinaryInfo{InstanceID: id, Binary: domain.BinaryInfo{Version: "26.11.9", Build: "native-build", GoVersion: "go1.27.3", OperatingSystem: "linux", Architecture: "amd64", ObservedAt: observed}}, nil
	})}
	response, err := handler.GetBinaryInfo(ctx, &xrayv1.GetBinaryInfoRequest{InstanceId: " edge "})
	if err != nil {
		t.Fatal(err)
	}
	info := response.GetBinary()
	if response.InstanceId != " edge " || info.GetVersion() != "26.11.9" || info.Build != "native-build" || info.GoVersion != "go1.27.3" || info.OperatingSystem != "linux" || info.Architecture != "amd64" || info.ObservedAtUnixMs != observed.UnixMilli() {
		t.Fatalf("native facts changed: %v", response)
	}
}

func TestBinaryInfoSanitizesErrorsAndDiscardsPartialFacts(t *testing.T) {
	for cause, expected := range map[error]codes.Code{
		instances.ErrNotFound:             codes.NotFound,
		domain.ErrInspectionDisabled:      codes.FailedPrecondition,
		domain.ErrInspectionBusy:          codes.ResourceExhausted,
		domain.ErrInfoOutputLimit:         codes.ResourceExhausted,
		domain.ErrInvalidInfoOutput:       codes.FailedPrecondition,
		context.Canceled:                  codes.Canceled,
		context.DeadlineExceeded:          codes.DeadlineExceeded,
		errors.New("native private path"): codes.Unavailable,
	} {
		handler := grpcapi.InfoAPI{AllowInfoRead: true, Info: binaryReader(func(context.Context, instances.InstanceID) (domain.InstanceBinaryInfo, error) {
			return domain.InstanceBinaryInfo{Binary: domain.BinaryInfo{Version: "partial"}}, fmt.Errorf("private diagnostics: %w", cause)
		})}
		response, err := handler.GetBinaryInfo(verifiedContext(t), &xrayv1.GetBinaryInfoRequest{InstanceId: "edge"})
		if response != nil || status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("error changed or leaked: %v, %v; want %v", response, err, expected)
		}
	}
	ctx, cancel := context.WithCancel(verifiedContext(t))
	defer cancel()
	handler := grpcapi.InfoAPI{AllowInfoRead: true, Info: binaryReader(func(context.Context, instances.InstanceID) (domain.InstanceBinaryInfo, error) {
		cancel()
		return domain.InstanceBinaryInfo{Binary: domain.BinaryInfo{Version: "partial"}}, nil
	})}
	if response, err := handler.GetBinaryInfo(ctx, &xrayv1.GetBinaryInfoRequest{InstanceId: "edge"}); response != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("canceled read became success: %v, %v", response, err)
	}
}
