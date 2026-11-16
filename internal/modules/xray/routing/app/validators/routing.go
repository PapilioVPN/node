package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

func BalancerTag(tag string) error {
	if strings.TrimSpace(tag) == "" || !utf8.ValidString(tag) {
		return domain.ErrInvalidBalancerTag
	}
	return nil
}

func BalancerOverride(selection domain.BalancerOverride) error {
	if BalancerTag(selection.BalancerTag) != nil || !utf8.ValidString(selection.Target) {
		return domain.ErrInvalidBalancerOverride
	}
	return nil
}

func RoutingRuleTag(tag string) error {
	// Native tags are opaque; whitespace can identify an actual configured rule.
	if tag == "" || !utf8.ValidString(tag) {
		return domain.ErrInvalidRoutingRuleTag
	}
	return nil
}

func RoutingRuleMode(mode domain.RoutingRuleMode) error {
	if mode != domain.AppendRoutingRule && mode != domain.ReplaceRoutingRules {
		return domain.ErrInvalidRoutingRuleMode
	}
	return nil
}
