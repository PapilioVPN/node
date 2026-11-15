package validators

import (
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func InboundUserSelection(selection domain.InboundUserSelection) error {
	if strings.TrimSpace(selection.InboundTag) == "" || strings.TrimSpace(selection.Email) == "" || !utf8.ValidString(selection.InboundTag) || !utf8.ValidString(selection.Email) {
		return domain.ErrInvalidUserSelection
	}
	return nil
}
