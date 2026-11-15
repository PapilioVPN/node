package system_test

import (
	"context"
	"slices"
	"testing"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/instances"
	instancedomain "github.com/PapilioVPN/node/internal/modules/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/system"
	"github.com/PapilioVPN/node/internal/modules/system/app"
	"github.com/PapilioVPN/node/internal/modules/system/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

func TestInstalledModuleCapabilities(t *testing.T) {
	for _, test := range []struct {
		name          string
		withInstances bool
	}{{name: "system alone"}, {name: "with instances", withInstances: true}} {
		t.Run(test.name, func(t *testing.T) {
			var info *app.InfoService
			options := []fx.Option{fx.NopLogger, fx.Supply(settings.Node{ID: "isolated-node"}), system.Module("test", false), fx.Populate(&info)}
			want := []domain.Capability{domain.ReadNodeInfo}
			if test.withInstances {
				// No engine or listener is needed to install the observation capability.
				options = append(options, fx.Supply([]settings.Instance{}), instances.Module(false))
				want = append(want, domain.Capability(instancedomain.ReadStatus))
			}
			application := fx.New(options...)
			if err := application.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := application.Stop(context.Background()); err != nil {
					t.Error(err)
				}
			})
			value, err := info.GetNodeInfo(context.Background())
			if err != nil || !slices.Equal(value.Capabilities, want) {
				t.Fatalf("operations do not match installed modules: %v, %v", value.Capabilities, err)
			}
		})
	}
}
