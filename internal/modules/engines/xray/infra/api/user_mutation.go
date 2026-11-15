package api

import (
	"context"
	"errors"

	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// Native ToTypedMessage discards marshal errors. Keep the error before dispatch.
func encodeUserOperation(message proto.Message) (*serial.TypedMessage, error) {
	payload, err := proto.Marshal(message)
	if err != nil {
		return nil, err
	}
	return &serial.TypedMessage{Type: serial.GetMessageType(message), Value: payload}, nil
}

func userMutationCause(ctx context.Context, err error) error {
	cause := domain.ErrUserMutationUnknown
	switch status.Code(err) {
	case codes.Unimplemented:
		cause = domain.ErrUserManagementUnsupported
	case codes.ResourceExhausted:
		cause = errors.Join(cause, domain.ErrAPIResourceLimit)
	case codes.Canceled:
		cause = errors.Join(cause, context.Canceled)
	case codes.DeadlineExceeded:
		cause = errors.Join(cause, context.DeadlineExceeded)
	}
	return errors.Join(cause, ctx.Err(), err)
}
