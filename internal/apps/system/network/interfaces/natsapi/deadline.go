package natsapi

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
)

func requestContext(ctx context.Context, headers nats.Header, timeout time.Duration) (context.Context, context.CancelFunc, string) {
	deadline := time.Now().Add(timeout)
	if values, ok := headers["Papilio-Deadline"]; ok {
		if len(values) != 1 {
			return nil, nil, "invalid_argument"
		}
		requested, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, "invalid_argument"
		}
		if requested.Before(deadline) {
			deadline = requested
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, ""
}
