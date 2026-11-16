package validators

import "github.com/PapilioVPN/node/internal/modules/xray/domain"

// UserEmail requires an identity without trimming an opaque native email.
func UserEmail(email domain.UserEmail) error {
	if email == "" {
		return domain.ErrInvalidUserEmail
	}
	return nil
}
