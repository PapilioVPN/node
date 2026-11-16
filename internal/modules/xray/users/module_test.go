package users_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/users"
	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
	userv1 "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi/proto/v1"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
)

type counter func(context.Context, string) (domain.InboundUserCount, error)

func (f counter) ReadInboundUserCount(ctx context.Context, tag string) (domain.InboundUserCount, error) {
	return f(ctx, tag)
}

type bindings struct {
	fx.Out
	Values []users.BackendBinding `group:"user_backends,flatten"`
}

func TestUserModuleBuildsFromFocusedPortsWithoutAnEngineModule(t *testing.T) {
	calls := 0
	var api grpcapi.UserAPI
	app := fx.New(fx.NopLogger, fx.Supply([]settings.Instance{}, &nativeapi.Resources{}),
		fx.Supply(settings.GRPCPermissions{ReadEngineUsers: true}),
		fx.Provide(func() bindings {
			return bindings{Values: []users.BackendBinding{
				{InstanceID: " edge ", Counter: counter(func(_ context.Context, tag string) (domain.InboundUserCount, error) {
					calls++
					if tag != " exact>>>tag " {
						t.Fatal("module changed native selection")
					}
					return domain.InboundUserCount{Count: 42, ObservedAt: time.Now()}, nil
				})},
				{InstanceID: "disabled"},
			}}
		}),
		users.Module(true), fx.Populate(&api),
	)
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	// Controlled identity here; native mTLS and SDK bindings are covered in bootstrap.
	ctx := peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
	response, err := api.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
	if err != nil || response.InstanceId != " edge " || response.InboundTag != " exact>>>tag " || response.Count != 42 || calls != 1 {
		t.Fatalf("module did not consume the selected port: %v", err)
	}
	for id, want := range map[string]codes.Code{"missing": codes.NotFound, "disabled": codes.FailedPrecondition} {
		response, err := api.GetInboundUserCount(ctx, &userv1.GetInboundUserCountRequest{InstanceId: id, InboundTag: "in"})
		if response != nil || status.Code(err) != want || calls != 1 {
			t.Fatalf("module lost absence/disabled binding: %v", err)
		}
	}
	if _, err := api.ListInboundUserMetadata(ctx, &userv1.ListInboundUserMetadataRequest{InstanceId: " edge ", InboundTag: "in"}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("count binding expanded metadata grants")
	}
}

func TestUserModuleRejectsAmbiguousBackendOwnership(t *testing.T) {
	var api grpcapi.UserAPI
	app := fx.New(fx.NopLogger, fx.Supply([]settings.Instance{}, &nativeapi.Resources{}), fx.Supply(settings.GRPCPermissions{}),
		fx.Provide(func() bindings {
			return bindings{Values: []users.BackendBinding{{InstanceID: "same"}, {InstanceID: "same"}}}
		}),
		users.Module(true), fx.Populate(&api),
	)
	if err := app.Err(); err == nil {
		t.Fatal("duplicate instance contributions were silently selected")
	}
}
