// Package grpcapi maps private Hysteria error causes to the public RPC contract.
package grpcapi

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
	"github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func ErrorStatus(err error) *status.Status {
	switch {
	case errors.Is(err, fault.ErrNotFound), errors.Is(err, observations.ErrNotFound):
		return status.New(codes.NotFound, fault.ErrNotFound.Error())
	case errors.Is(err, fault.ErrInvalid), errors.Is(err, observations.ErrInvalid):
		return status.New(codes.InvalidArgument, fault.ErrInvalid.Error())
	case errors.Is(err, fault.ErrTooLarge):
		return status.New(codes.ResourceExhausted, fault.ErrTooLarge.Error())
	case errors.Is(err, fault.ErrBusy):
		return status.New(codes.Aborted, fault.ErrBusy.Error())
	case errors.Is(err, fault.ErrInactive), errors.Is(err, observations.ErrInactive), errors.Is(err, live.ErrInactive):
		return status.New(codes.FailedPrecondition, fault.ErrInactive.Error())
	case errors.Is(err, observations.ErrCapacity), errors.Is(err, live.ErrCapacity):
		return status.New(codes.ResourceExhausted, "Hysteria operation capacity reached")
	case errors.Is(err, observations.ErrTooLarge):
		return status.New(codes.ResourceExhausted, observations.ErrTooLarge.Error())
	case errors.Is(err, observations.ErrEffectUnknown):
		return status.New(codes.Unavailable, observations.ErrEffectUnknown.Error())
	case errors.Is(err, context.Canceled):
		return status.New(codes.Canceled, "Hysteria operation canceled")
	case errors.Is(err, context.DeadlineExceeded):
		return status.New(codes.DeadlineExceeded, "Hysteria operation deadline exceeded")
	default:
		return status.New(codes.Unavailable, fault.ErrUnavailable.Error())
	}
}
