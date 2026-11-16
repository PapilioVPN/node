package api

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func routingMutationCause(ctx context.Context, err error) error {
	cause := domain.ErrRoutingMutationUnknown
	switch status.Code(err) {
	case codes.Unimplemented:
		cause = domain.ErrRoutingManagementUnsupported
	case codes.ResourceExhausted:
		cause = errors.Join(cause, domain.ErrAPIResourceLimit)
	case codes.Canceled:
		cause = errors.Join(cause, context.Canceled)
	case codes.DeadlineExceeded:
		cause = errors.Join(cause, context.DeadlineExceeded)
	}
	return errors.Join(cause, ctx.Err(), err)
}
