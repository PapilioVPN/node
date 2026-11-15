package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func BalancerTag(tag string) error {
	if strings.TrimSpace(tag) == "" || !utf8.ValidString(tag) {
		return domain.ErrInvalidBalancerTag
	}
	return nil
}
