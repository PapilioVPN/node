package settings_test

import (
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestTrafficResetGrantIsIndependentAndOptIn(t *testing.T) {
	cfg, err := settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   read_engine_stats: true
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Communications.GRPC.Permissions.ResetEngineStats {
		t.Fatal("traffic reads granted reset")
	}
	cfg, err = settings.Load(writeConfig(t, `node: {id: west}
communications:
 grpc:
  permissions:
   reset_engine_stats: true
`))
	if err != nil {
		t.Fatal(err)
	}
	grants := cfg.Communications.GRPC.Permissions
	if !grants.ResetEngineStats || grants.ReadEngineStats || grants.ReadEngineInfo || grants.ReadEngineOnline || grants.ControlInstances {
		t.Fatal("reset grant expanded other permissions")
	}
}
