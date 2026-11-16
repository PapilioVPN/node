package validators_test

import (
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/xray/routing/app/validators"
	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

func TestBalancerOverrideAcceptsExplicitClearAndOpaqueTargets(t *testing.T) {
	for _, target := range []string{"", "   ", " outside>>>selector "} {
		if err := validators.BalancerOverride(domain.BalancerOverride{BalancerTag: " exact>>>pool ", Target: target}); err != nil {
			t.Fatalf("valid clear/opaque target rejected: %v", err)
		}
	}
	for _, selection := range []domain.BalancerOverride{{}, {BalancerTag: " "}, {BalancerTag: "\xff"}, {BalancerTag: "pool", Target: "\xff"}} {
		if err := validators.BalancerOverride(selection); !errors.Is(err, domain.ErrInvalidBalancerOverride) {
			t.Fatalf("invalid override accepted: %v", err)
		}
	}
}

func TestRoutingRuleTagsPreserveNativeWhitespaceAndRequireNonemptyUTF8(t *testing.T) {
	for _, tag := range []string{" rule>>>tag ", "   "} {
		if err := validators.RoutingRuleTag(tag); err != nil {
			t.Fatal(err)
		}
	}
	for _, tag := range []string{"", "\xff"} {
		if err := validators.RoutingRuleTag(tag); !errors.Is(err, domain.ErrInvalidRoutingRuleTag) {
			t.Fatalf("invalid tag accepted: %v", err)
		}
	}
}
