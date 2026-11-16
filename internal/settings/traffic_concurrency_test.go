package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestTrafficResetConcurrencyRequiresAnExplicitBoundWithinAPICapacity(t *testing.T) {
	api := "{network: tcp, address: '127.0.0.1:12345', request_timeout: 1s, max_receive_bytes: 4096, max_concurrent: 4}"
	for _, profile := range []struct {
		value string
		valid bool
	}{
		{"{reset_concurrency: 1}", true}, {"{reset_concurrency: 3}", true}, {"{reset_concurrency: 4}", true},
		{"{}", false}, {"{reset_concurrency: 0}", false}, {"{reset_concurrency: -1}", false}, {"{reset_concurrency: 5}", false},
	} {
		cfg, err := settings.Load(writeConfig(t, apiConfiguration(api)+"    traffic: "+profile.value+"\n"))
		if !profile.valid {
			if !errors.Is(err, settings.ErrInvalidConfig) {
				t.Fatalf("invalid reset concurrency accepted: %v", err)
			}
			continue
		}
		if err != nil || cfg.Instances[0].Traffic == nil || cfg.Communications.GRPC.Permissions.ResetEngineStats {
			t.Fatalf("profile loading failed or granted reset: %v", err)
		}
	}
	_, err := settings.Load(writeConfig(t, strings.Replace(apiConfiguration(api), "      api: "+api+"\n", "", 1)+"    traffic: {reset_concurrency: 1}\n"))
	if !errors.Is(err, settings.ErrInvalidConfig) {
		t.Fatal("concurrent resets accepted without an API")
	}
	cfg, err := settings.Load(writeConfig(t, apiConfiguration(api)))
	if err != nil || cfg.Instances[0].Traffic != nil {
		t.Fatalf("omission no longer preserves previous settings: %v", err)
	}
}
