package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi"
	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type readerFunc func(context.Context) (domain.Identity, error)

func (read readerFunc) Read(ctx context.Context) (domain.Identity, error) { return read(ctx) }

// This controlled peer context exercises mapping only, not certificate verification.
func verifiedPeer() context.Context {
	return peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
}

func TestReadChecksAuthenticationAndPermissionBeforeService(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second}
	if _, err := handler.GetHostIdentity(context.Background(), &identityv1.GetHostIdentityRequest{}); status.Code(err) != codes.Unauthenticated {
		t.Fatal(err)
	}
	if _, err := handler.GetHostIdentity(verifiedPeer(), &identityv1.GetHostIdentityRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
}

func TestReadPreservesCancellationAndSanitizesNativeFailure(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{errors.New("private-native-cause"), codes.Unavailable},
	} {
		handler := grpcapi.Handler{AllowRead: true, Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Identity, error) { return domain.Identity{}, test.cause })}}
		result, err := handler.GetHostIdentity(verifiedPeer(), &identityv1.GetHostIdentityRequest{})
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-native-cause") {
			t.Fatalf("invalid failure: %v %v", result, err)
		}
	}
}
