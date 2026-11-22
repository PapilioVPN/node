package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

// Reset checks explicit intent and method-specific selector bounds.
func Reset(r domain.ResetRequest) error {
	if len(r.Epoch) != 32 || r.OperationID == "" || len(r.OperationID) > 128 || !utf8.ValidString(r.OperationID) || strings.ContainsRune(r.OperationID, 0) {
		return domain.ErrInvalidArgument
	}
	selection := r.Selection
	switch r.Kind {
	case domain.ResetExact:
		if r.MaxCounters != 0 {
			return domain.ErrInvalidArgument
		}
	case domain.ResetPair:
		if selection.Direction != "" || r.MaxCounters != 0 {
			return domain.ErrInvalidArgument
		}
		selection.Direction = domain.Uplink
	case domain.ResetScope:
		if selection.Name != "" || selection.Direction != "" || r.MaxCounters == 0 || r.MaxCounters > domain.MaxResetCounters {
			return domain.ErrInvalidArgument
		}
		selection.Name = "scope"
		selection.Direction = domain.Uplink
	default:
		return domain.ErrInvalidArgument
	}
	return Selection(selection)
}
