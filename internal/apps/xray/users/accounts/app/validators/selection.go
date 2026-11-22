// Package validators owns exact native account selectors.
package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func Selector(value string) error {
	if value == "" || len(value) > 4096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return domain.ErrInvalidArgument
	}
	return nil
}
