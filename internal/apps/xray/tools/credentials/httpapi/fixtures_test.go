package httpapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

type generator struct {
	services.Generator
	calls int
}

func (g *generator) UUID(ctx context.Context, input domain.UUIDInput) (domain.UUIDResult, error) {
	g.calls++
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	return domain.UUIDResult{UUID: "fixture-private"}, nil
}
