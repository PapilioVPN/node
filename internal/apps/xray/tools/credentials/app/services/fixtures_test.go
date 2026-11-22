package services

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

type generator struct {
	Generator
	uuid func(context.Context, domain.UUIDInput) (domain.UUIDResult, error)
}

func (g generator) UUID(ctx context.Context, input domain.UUIDInput) (domain.UUIDResult, error) {
	return g.uuid(ctx, input)
}
