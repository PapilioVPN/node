//go:build integration && linux

package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	inboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	inboundapi "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	inboundconfiguration "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	configservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configinspection "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/configuration"
	configprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/process"
	configstate "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/state"
	instanceservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	accountservices "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	accountapi "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	accountconfiguration "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	accountinbounds "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/inbounds"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	accountadapter "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	configadapter "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/configs"
)

type countCompiler struct {
	source *configservices.Service
	calls  int
}

func (c *countCompiler) Compile(ctx context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
	c.calls++
	return c.source.Compile(ctx, id, sources)
}

type fullFixture struct {
	*fixture
	compiler    *countCompiler
	configState *configservices.StateService
	binding     configadapter.V1
}

func withFull(t *testing.T, f *fixture) *fullFixture {
	t.Helper()
	admission, err := configprocess.NewAdmission(2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admission.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	builder, err := configprocess.New(configprocess.Config{Binary: f.binary, Directory: filepath.Dir(f.baseline), WorkDirectory: f.workDirectory}, admission)
	if err != nil {
		t.Fatal(err)
	}
	preparation, err := configservices.New([]configservices.Binding{{ID: "one", Builder: builder, Compiler: builder, SourceBuilder: builder}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := configservices.NewState([]configservices.StateBinding{{ID: "one", Repository: f.store}}, preparation, f.instances)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	compiler := &countCompiler{source: preparation}
	plans, err := configservices.NewPlans(state, compiler, configinspection.Inspector{}, f.instances, []configservices.PlanBinding{{ID: "one", Repository: f.store, Baseline: configprocess.Baseline{Path: f.baseline}}})
	if err != nil {
		t.Fatal(err)
	}
	memberships, err := configservices.NewMemberships(plans, accountconfiguration.ReplaceUsers, configinspection.Embeddable)
	if err != nil {
		t.Fatal(err)
	}
	binding := configadapter.V1{Source: memberships}
	service, err := services.New([]string{"one"}, accountadapter.Observer{Source: f.accounts, Catalog: f.selection, Configs: binding}, f.accounts, binding, accountadapter.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	f.service = service
	return &fullFixture{fixture: f, compiler: compiler, configState: state, binding: binding}
}

func deadline(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// restartOwners closes every native process/channel, the source lock and the
// application owners, then reconstructs them against the same private directory.
// It deliberately permits a failed durable candidate to start unready, so its
// persisted Configs recovery can be exercised without a volatile sync receipt.
func restartOwners(t *testing.T, old *fullFixture) *fullFixture {
	t.Helper()
	ctx, cancel := deadline(t)
	defer cancel()
	if err := old.configState.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.instances.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := old.store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, err := configstate.New(old.stateDirectory, "one")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Open(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	connection, err := rpc.New(rpc.Config{Network: "tcp", Address: old.native})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	runtime, err := instanceprocess.New(instanceprocess.Config{ID: "one", Binary: old.binary, Configuration: old.baseline, StopTimeout: time.Second, API: connection, Source: configprocess.Resolver{Reader: store, WorkDirectory: old.workDirectory}})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := instanceservices.New([]instanceservices.Binding{{ID: "one", Engine: "xray", Runtime: runtime}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := instances.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if _, err := instances.Start(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	counter := &calls{ClientConnInterface: connection, methods: make(map[string]int)}
	nativeInbounds := inboundapi.New(counter)
	inbounds, err := inboundservices.New([]inboundservices.Binding{{ID: "one", Client: nativeInbounds, Projection: nativeInbounds}}, instances)
	if err != nil {
		t.Fatal(err)
	}
	selection := accountinbounds.Reader{Source: inboundservices.NewCatalog(inbounds, inboundconfiguration.Inspector{})}
	nativeAccounts := accountapi.New(counter)
	accounts, err := accountservices.New([]accountservices.Binding{{ID: "one", Client: nativeAccounts, Reader: nativeAccounts}}, instances, selection, accountapi.Inspector{})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{calls: counter, instances: instances, address: old.address, target: old.target, accounts: accounts, native: old.native, store: store, stateDirectory: old.stateDirectory, workDirectory: old.workDirectory, binary: old.binary, baseline: old.baseline, selection: selection, excluded: old.excluded}
	return withFull(t, f)
}
