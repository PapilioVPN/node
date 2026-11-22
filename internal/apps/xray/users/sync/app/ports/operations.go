package ports

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

type Operations interface {
	Revision(context.Context, string) (domain.Revision, error)
	Preview(context.Context, domain.Request) (domain.Preview, error)
	Apply(context.Context, string, string, domain.Revision) (domain.ReceiptPage, error)
	Status(context.Context, string, string, int) (domain.ReceiptPage, error)
	Refresh(context.Context, string, string) (domain.ReceiptPage, error)
	Recover(context.Context, string, string, uint64) (domain.ReceiptPage, error)
}
