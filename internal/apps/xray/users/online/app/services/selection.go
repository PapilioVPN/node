package services

import (
	"strings"
	"unicode/utf8"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

func validateSelection(s domain.Selection) error {
	if instances.ID(s.InstanceID) != nil || s.Email == "" || len(s.Email) > 4096 || !utf8.ValidString(s.Email) || strings.ContainsRune(s.Email, 0) {
		return domain.ErrInvalidArgument
	}
	return nil
}
