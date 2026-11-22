// Package validators owns exact native account selectors.
package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func Selector(value string) error {
	if value == "" {
		return domain.ErrInvalidArgument
	}
	return Email(value)
}

// Email retains native empty-email semantics for explicit mutations.
func Email(value string) error {
	if len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return domain.ErrInvalidArgument
	}
	return nil
}

func Configuration(content []byte) error {
	if len(content) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(content) > domain.MaxAccountBytes {
		return domain.ErrCapacity
	}
	return nil
}
