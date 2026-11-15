// Package grpcresponse owns protocol primitives shared by capability adapters.
package grpcresponse

import (
	"context"
	"errors"

	"google.golang.org/grpc/status"
)

// ContextError maps cancellation without exposing a wrapping native error's text.
// A non-context failure returns nil for the capability adapter to classify.
func ContextError(cause error) error {
	if errors.Is(cause, context.Canceled) {
		return status.FromContextError(context.Canceled).Err()
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		return status.FromContextError(context.DeadlineExceeded).Err()
	}
	return nil
}
