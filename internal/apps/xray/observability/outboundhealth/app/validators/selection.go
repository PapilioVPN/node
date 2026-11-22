// Package validators checks untrusted outbound health selections.
package validators

import (
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
)

// Tag accepts an explicitly selected anonymous outbound and opaque whitespace.
func Tag(tag string) error {
	if len(tag) > domain.MaxTagBytes || !utf8.ValidString(tag) {
		return domain.ErrInvalidArgument
	}
	return nil
}
func MaxAge(seconds uint32) (uint32, error) {
	if seconds > domain.MaxAgeSeconds {
		return 0, domain.ErrInvalidArgument
	}
	if seconds == 0 {
		seconds = domain.DefaultMaxAgeSeconds
	}
	return seconds, nil
}
