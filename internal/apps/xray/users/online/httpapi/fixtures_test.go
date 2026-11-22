package httpapi

import (
	"context"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

type reader struct{ calls int }

func (r *reader) Count(ctx context.Context, email string) (domain.Summary, error) {
	r.calls++
	if _, ok := ctx.Deadline(); !ok {
		panic("missing deadline")
	}
	v := int64(3)
	return domain.Summary{Count: &v}, nil
}
func (r *reader) IPs(context.Context, string) (domain.Visitors, error) {
	r.calls++
	return domain.Visitors{Tracked: true, IPs: []domain.IP{{Address: "native", LastSeenUnixSeconds: -1}}}, nil
}

type statusReader struct{}

func (statusReader) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 9}}, nil
}
