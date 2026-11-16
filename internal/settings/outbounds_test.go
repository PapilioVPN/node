package settings_test

import (
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestOutboundReadGrantIsIndependentAndOptIn(t *testing.T) {
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
   read_engine_routing: true
   test_engine_routing: true
   manage_engine_routing: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.Permissions.ReadEngineOutbounds {
		t.Fatal("other grants enabled outbound observations")
	}
	config, err = settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_engine_outbounds: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.Permissions != (settings.GRPCPermissions{ReadEngineOutbounds: true}) {
		t.Fatal("outbound grant did not load independently")
	}
}
