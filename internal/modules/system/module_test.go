package system_test

import (
	"context"
	"slices"
	"testing"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/core"
	instancedomain "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/system"
	"github.com/PapilioVPN/node/internal/modules/system/info/app/services"
	"github.com/PapilioVPN/node/internal/modules/system/info/domain"
	"github.com/PapilioVPN/node/internal/settings"
)

func TestInstalledModuleCapabilities(t *testing.T) {
	for _, test := range []struct {
		name          string
		withInstances bool
	}{{name: "system alone"}, {name: "with instances", withInstances: true}} {
		t.Run(test.name, func(t *testing.T) {
			var info *services.InfoService
			options := []fx.Option{fx.NopLogger, fx.Supply(settings.Node{ID: "isolated-node"}), system.Module("test", settings.Communications{}), fx.Populate(&info)}
			want := []domain.Capability{domain.ReadNodeInfo}
			if test.withInstances {
				// No engine or listener is needed to install the lifecycle capabilities.
				options = append(options, fx.Supply([]settings.Instance{}), core.Module(false))
				want = append(want, domain.Capability(instancedomain.StartInstance), domain.Capability(instancedomain.ListStatuses), domain.Capability(instancedomain.ReadStatus), domain.Capability(instancedomain.StopInstance))
				want = append(want, domain.Capability(instancedomain.ValidateConfig))
				slices.Sort(want[1:])
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
