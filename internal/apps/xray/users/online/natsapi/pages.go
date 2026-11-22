package natsapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/infra/jsonwire"
	"github.com/nats-io/nats.go"
)

func (h Handler) page(ctx context.Context, data []byte, headers nats.Header, operation func(context.Context, domain.PageRequest) (domain.Page, error), ips bool) []byte {
	r, err := jsonwire.DecodePage(data, ips)
	if err != nil {
		return failure(err)
	}
	ctx, cancel, err := deadline(ctx, headers, h.Timeout)
	if err != nil {
		return failure(err)
	}
	defer cancel()
	p, err := operation(ctx, r)
	if err != nil {
		return failure(err)
	}
	if ips {
		data, err = jsonwire.EncodeIPPage(p)
	} else {
		data, err = jsonwire.EncodePage(p)
	}
	if err != nil {
		return failure(err)
	}
	return data
}
