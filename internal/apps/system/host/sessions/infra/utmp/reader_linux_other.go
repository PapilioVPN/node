//go:build linux && !amd64

package utmp

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

// Additional legacy binary ABIs require their own native layout verification.
func (Reader) List(context.Context, domain.Selection) (domain.RecordPage, error) {
	return domain.RecordPage{}, domain.ErrUnsupported
}
