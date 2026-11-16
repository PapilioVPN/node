package settings_test

import (
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestUserObservationGrantsAreIndependentAndOptIn(t *testing.T) {
	for _, tc := range []struct {
		yaml string
		want settings.GRPCPermissions
	}{
		{"", settings.GRPCPermissions{}},
		{"   read_engine_user_metadata: true\n", settings.GRPCPermissions{ReadEngineUserMetadata: true}},
		{"   read_engine_user_accounts: true\n", settings.GRPCPermissions{ReadEngineUserAccounts: true}},
		{"   read_engine_users: true\n", settings.GRPCPermissions{ReadEngineUsers: true}},
	} {
		cfg, err := settings.Load(writeConfig(t, "node: {id: west}\ncommunications:\n grpc:\n  permissions:\n"+tc.yaml))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Communications.GRPC.Permissions != tc.want {
			t.Fatal("user count, metadata and account grants expanded one another")
		}
	}
	cfg, err := settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_instance_status: true
   control_instances: true
   validate_instance_config: true
   read_engine_info: true
   read_engine_stats: true
   reset_engine_stats: true
   manage_engine_users: true
   read_engine_users: true
   read_engine_online: true
   read_engine_routing: true
   read_engine_outbounds: true
   test_engine_routing: true
   manage_engine_routing: true
`))
	if err != nil {
		t.Fatal(err)
	}
	p := cfg.Communications.GRPC.Permissions
	if p.ReadEngineUserMetadata || p.ReadEngineUserAccounts {
		t.Fatal("prior grants enabled account or metadata reads")
	}
}
