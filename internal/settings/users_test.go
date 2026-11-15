package settings_test

import (
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestEngineUserManagementGrantIsIndependentAndOptIn(t *testing.T) {
	config, err := settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_engine_info: true
   read_engine_stats: true
   read_engine_online: true
   reset_engine_stats: true
   control_instances: true
   validate_instance_config: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Communications.GRPC.Permissions.ManageEngineUsers {
		t.Fatal("other permissions enabled user changes")
	}
	config, err = settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   manage_engine_users: true
`))
	if err != nil {
		t.Fatal(err)
	}
	grants := config.Communications.GRPC.Permissions
	if !grants.ManageEngineUsers || grants.ControlInstances || grants.ValidateInstanceConfig || grants.ReadEngineInfo || grants.ReadEngineStats || grants.ReadEngineOnline || grants.ResetEngineStats || grants.ReadInstanceStatus {
		t.Fatal("user management expanded other grants")
	}
}
