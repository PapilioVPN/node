package online_test

import (
	"context"
	"testing"
	"time"

	"go.uber.org/fx"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online"
	"github.com/PapilioVPN/node/internal/modules/xray/online/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi"
	onlinev1 "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi/proto/v1"
	nativeapi "github.com/PapilioVPN/node/internal/platform/xray"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/testutil"
)

type reader struct {
	count func(context.Context, domain.UserEmail) (domain.UserOnline, error)
}

func (r reader) ReadUserOnlineStats(ctx context.Context, email domain.UserEmail) (domain.UserOnline, error) {
	return r.count(ctx, email)
}
func (reader) ReadUserOnlineIPs(context.Context, domain.UserEmail) (domain.UserOnlineIPs, error) {
	panic("unexpected IP observation")
}
func (reader) ReadOnlineUsers(context.Context) (domain.OnlineUsers, error) {
	panic("unexpected online list")
}

type bindings struct {
	fx.Out
	Values []online.BackendBinding `group:"online_backends,flatten"`
}

func TestOnlineModuleConsumesFocusedPortsWithoutUserManagement(t *testing.T) {
	calls := 0
	var api grpcapi.OnlineAPI
	app := fx.New(fx.NopLogger, fx.Supply([]settings.Instance{}, &nativeapi.Resources{}),
		fx.Supply(settings.GRPCPermissions{ReadEngineOnline: true}),
		fx.Provide(func() bindings {
			return bindings{Values: []online.BackendBinding{
				{InstanceID: " edge ", Reader: reader{count: func(_ context.Context, email domain.UserEmail) (domain.UserOnline, error) {
					calls++
					if email != " Mixed>>>@Example " {
						t.Fatal("module changed native identity")
					}
					return domain.UserOnline{OnlineIPCount: 42, ObservedAt: time.UnixMilli(123)}, nil
				}}},
				{InstanceID: "disabled"},
			}}
		}), online.Module(true), fx.Populate(&api),
	)
	if err := app.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx := testutil.VerifiedGRPCContext(t)
	response, err := api.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: " edge ", Email: " Mixed>>>@Example "})
	if err != nil || response.InstanceId != " edge " || response.Email != " Mixed>>>@Example " || response.OnlineIpCount != 42 || response.ObservedAtUnixMs != 123 || calls != 1 {
		t.Fatalf("module lost selected observation: %v", err)
	}
	for id, want := range map[string]codes.Code{"missing": codes.NotFound, "disabled": codes.FailedPrecondition} {
		response, err := api.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: id, Email: "reader"})
		if response != nil || status.Code(err) != want || calls != 1 {
			t.Fatal("module lost absence/disabled distinction")
		}
	}
}

func TestOnlineModuleRejectsAmbiguousBackendOwnership(t *testing.T) {
	for _, values := range [][]online.BackendBinding{{{InstanceID: "same"}, {InstanceID: "same"}}, {{InstanceID: instances.InstanceID("")}}} {
		var api grpcapi.OnlineAPI
		app := fx.New(fx.NopLogger, fx.Supply([]settings.Instance{}, &nativeapi.Resources{}), fx.Supply(settings.GRPCPermissions{}),
			fx.Provide(func() bindings { return bindings{Values: values} }), online.Module(true), fx.Populate(&api),
		)
		if app.Err() == nil {
			t.Fatal("ambiguous or blank instance ownership accepted")
		}
	}
}
