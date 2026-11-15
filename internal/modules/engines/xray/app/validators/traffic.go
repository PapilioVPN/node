// Package validators owns pure Xray application input checks.
package validators

import (
	"strings"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// TrafficSelection validates boundary input without changing its identity.
func TrafficSelection(selection domain.TrafficSelection) error {
	if strings.TrimSpace(selection.Name) == "" {
		return domain.ErrInvalidTrafficSelection
	}
	if err := TrafficScope(selection.Scope); err != nil {
		return err
	}
	if selection.Direction != domain.Uplink && selection.Direction != domain.Downlink {
		return domain.ErrInvalidTrafficSelection
	}
	return nil
}

// TrafficScope checks a collection scope without normalizing it.
func TrafficScope(scope domain.TrafficScope) error {
	if scope != domain.UserTraffic && scope != domain.InboundTraffic && scope != domain.OutboundTraffic {
		return domain.ErrInvalidTrafficSelection
	}
	return nil
}
