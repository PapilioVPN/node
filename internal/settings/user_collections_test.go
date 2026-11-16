package settings_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestUserCollectionsRequireAnExplicitPositiveLimitAndNativeAPI(t *testing.T) {
	api := "{network: tcp, address: '127.0.0.1:12345', request_timeout: 1s, max_receive_bytes: 4096, max_concurrent: 4}"
	for _, profile := range []struct {
		value string
		valid bool
	}{
		{"{max_batch_users: 1}", true}, {"{max_batch_users: 1000}", true}, {"{max_batch_users: 4294967295}", true},
		{"{}", false}, {"{max_batch_users: 0}", false}, {"{max_batch_users: -1}", false}, {"{max_batch_users: 4294967296}", false},
	} {
		cfg, err := settings.Load(writeConfig(t, apiConfiguration(api)+"      users: "+profile.value+"\n"))
		if !profile.valid {
			if !errors.Is(err, settings.ErrInvalidConfig) {
				t.Fatalf("invalid user collection limit accepted: %v", err)
			}
			continue
		}
		if err != nil || cfg.Instances[0].Xray.Users == nil || cfg.Communications.GRPC.Permissions.ManageEngineUsers {
			t.Fatal("explicit collection profile failed or granted user writes")
		}
	}
	_, err := settings.Load(writeConfig(t, strings.Replace(apiConfiguration(api), "      api: "+api+"\n", "", 1)+"      users: {max_batch_users: 1000}\n"))
	if !errors.Is(err, settings.ErrInvalidConfig) {
		t.Fatal("collection profile accepted without native API")
	}
	cfg, err := settings.Load(writeConfig(t, apiConfiguration(api)))
	if err != nil || cfg.Instances[0].Xray.Users != nil {
		t.Fatal("omission enabled user collection mutations")
	}
}
