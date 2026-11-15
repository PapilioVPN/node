package settings_test

import (
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestRoutingReadGrantIsIndependentAndOptIn(t *testing.T) {
	config, err := settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_instance_status: true
   control_instances: true
   validate_instance_config: true
   read_engine_info: true
   read_engine_stats: true
   read_engine_users: true
   read_engine_online: true
   reset_engine_stats: true
   manage_engine_users: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.Permissions.ReadEngineRouting {
		t.Fatal("other grants implicitly enabled routing observations")
	}
	config, err = settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_engine_routing: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.Permissions != (settings.GRPCPermissions{ReadEngineRouting: true}) {
		t.Fatal("routing grant did not load independently")
	}
}
