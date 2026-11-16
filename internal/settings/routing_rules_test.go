package settings_test

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

const ruleAPI = "{network: unix, address: api.sock, request_timeout: 2s, max_receive_bytes: 4096, max_concurrent: 2}"
const ruleProfile = "{work_dir: routing-work, max_input_bytes: 8192, max_output_bytes: 65536, max_concurrent: 1}"

func TestRoutingRulePreparationLoadsWithoutProvisioningOrGrantingAccess(t *testing.T) {
	path := writeConfig(t, apiConfiguration(ruleAPI)+"      routing_rules: "+ruleProfile+"\n")
	config, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rules := config.Instances[0].Xray.RoutingRules
	if rules == nil || rules.WorkDir != filepath.Join(filepath.Dir(path), "routing-work") || rules.MaxInputBytes != 8192 || rules.MaxOutputBytes != 65536 || rules.MaxConcurrent != 1 {
		t.Fatalf("rule profile changed: %+v", rules)
	}
	if config.Communications.GRPC.Permissions.ManageEngineRouting {
		t.Fatal("preparation profile granted external routing management")
	}
	if _, err := os.Stat(rules.WorkDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loading settings provisioned work directory: %v", err)
	}
	legacy, err := settings.Load(writeConfig(t, apiConfiguration(ruleAPI)))
	if err != nil || legacy.Instances[0].Xray.RoutingRules != nil {
		t.Fatalf("legacy instance settings changed: %+v %v", legacy, err)
	}
}

func TestRoutingRulePreparationRequiresAPIAndExplicitSafeBounds(t *testing.T) {
	for _, profile := range []string{"{}", strings.Replace(ruleProfile, "routing-work", "' '", 1), strings.Replace(ruleProfile, "8192", "0", 1), strings.Replace(ruleProfile, "65536", "0", 1), strings.Replace(ruleProfile, "65536", fmt.Sprint(math.MaxInt), 1), strings.Replace(ruleProfile, "max_concurrent: 1", "max_concurrent: -1", 1), strings.Replace(ruleProfile, "}", ", timeout: 1s}", 1)} {
		_, err := settings.Load(writeConfig(t, apiConfiguration(ruleAPI)+"      routing_rules: "+profile+"\n"))
		if !errors.Is(err, settings.ErrInvalidConfig) {
			t.Fatalf("unbounded/inconsistent preparation profile accepted: %s %v", profile, err)
		}
	}
	withoutAPI := strings.Replace(apiConfiguration(ruleAPI), "      api: "+ruleAPI+"\n", "", 1) + "      routing_rules: " + ruleProfile + "\n"
	if _, err := settings.Load(writeConfig(t, withoutAPI)); !errors.Is(err, settings.ErrInvalidConfig) {
		t.Fatalf("rule application declared without API: %v", err)
	}
}
