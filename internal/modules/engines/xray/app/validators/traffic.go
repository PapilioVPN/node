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
	if selection.Scope != domain.UserTraffic && selection.Scope != domain.InboundTraffic && selection.Scope != domain.OutboundTraffic {
		return domain.ErrInvalidTrafficSelection
	}
	if selection.Direction != domain.Uplink && selection.Direction != domain.Downlink {
		return domain.ErrInvalidTrafficSelection
	}
	return nil
}
