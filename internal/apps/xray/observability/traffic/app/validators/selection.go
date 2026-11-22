// Package validators owns pure traffic selector bounds.
package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

// Selection preserves exact spelling, whitespace and embedded native separators.
// The 4096-byte bound applies to the opaque identity, not engine inventory size.
func Selection(selection domain.Selection) error {
	switch selection.Scope {
	case domain.User, domain.Inbound, domain.Outbound:
	default:
		return domain.ErrInvalidArgument
	}
	switch selection.Direction {
	case domain.Uplink, domain.Downlink:
	default:
		return domain.ErrInvalidArgument
	}
	if selection.Name == "" || len(selection.Name) > 4096 || !utf8.ValidString(selection.Name) || strings.ContainsRune(selection.Name, 0) {
		return domain.ErrInvalidArgument
	}
	return nil
}
