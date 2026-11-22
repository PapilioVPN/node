// Package validators owns the actual live outbound request bounds.
package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

func Configuration(content []byte) error {
	if len(content) == 0 {
		return domain.ErrInvalidArgument
	}
	if len(content) > domain.MaxConfigurationBytes {
		return domain.ErrCapacity
	}
	return nil
}
func Tag(tag string) error {
	if tag == "" || len(tag) > 4096 || !utf8.ValidString(tag) || strings.ContainsRune(tag, 0) {
		return domain.ErrInvalidArgument
	}
	return nil
}
