package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

func requestContext(ctx context.Context, data []byte, headers nats.Header, timeout time.Duration) (context.Context, context.CancelFunc, string) {
	if len(data) > 1024 {
		return nil, nil, "resource_exhausted"
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) != 0 {
		return nil, nil, "invalid_argument"
	}
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
