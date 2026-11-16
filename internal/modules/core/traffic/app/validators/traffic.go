// Package validators owns pure traffic input checks.
package validators

import (
	"strings"

	"github.com/PapilioVPN/node/internal/modules/core/traffic/domain"
)

// TrafficSelection validates boundary input without changing its identity.
func TrafficSelection(selection domain.TrafficSelection) error {
	if err := TrafficPairSelection(domain.TrafficPairSelection{Scope: selection.Scope, Name: selection.Name}); err != nil {
		return err
	}
	if selection.Direction != domain.Uplink && selection.Direction != domain.Downlink {
		return domain.ErrInvalidTrafficSelection
	}
	return nil
}

// TrafficPairSelection checks an opaque email/tag without normalizing it.
func TrafficPairSelection(selection domain.TrafficPairSelection) error {
	if strings.TrimSpace(selection.Name) == "" {
		return domain.ErrInvalidTrafficSelection
	}
	return TrafficScope(selection.Scope)
}

// TrafficScope checks a collection scope without normalizing it.
func TrafficScope(scope domain.TrafficScope) error {
	if scope != domain.UserTraffic && scope != domain.InboundTraffic && scope != domain.OutboundTraffic {
		return domain.ErrInvalidTrafficSelection
	}
	return nil
}
