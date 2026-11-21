// Package validators owns pure instance selection checks.
package validators

import "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"

// ID is an opaque URL/subject-safe deployment identity, shared by all boundaries.
func ID(value string) error {
	if len(value) == 0 || len(value) > 128 {
		return domain.ErrInvalidArgument
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return domain.ErrInvalidArgument
		}
	}
	return nil
}
