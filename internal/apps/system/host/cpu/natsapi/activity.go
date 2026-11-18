package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

func (handler Handler) ReadActivity(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) != 0 {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.ReadActivity(ctx)
	if err != nil {
		return readError(err)
	}
	encoded, err := json.Marshal(activityResponse{
		ContextSwitches: result.Activity.ContextSwitches,
		Interrupts:      result.Activity.Interrupts,
		SoftInterrupts:  result.Activity.SoftInterrupts,
		CreatedTasks:    result.Activity.CreatedTasks,
		RunnableTasks:   result.Activity.RunnableTasks,
		IOBlockedTasks:  result.Activity.IOBlockedTasks,
		ObservedAt:      result.ObservedAt, DurationNS: int64(result.Duration),
	})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

type activityResponse struct {
	ContextSwitches uint64    `json:"context_switches,string"`
	Interrupts      uint64    `json:"interrupts,string"`
	SoftInterrupts  uint64    `json:"soft_interrupts,string"`
	CreatedTasks    uint64    `json:"created_tasks,string"`
	RunnableTasks   uint64    `json:"runnable_tasks,string"`
	IOBlockedTasks  uint64    `json:"io_blocked_tasks,string"`
	ObservedAt      time.Time `json:"observed_at"`
	DurationNS      int64     `json:"duration_ns,string"`
}
