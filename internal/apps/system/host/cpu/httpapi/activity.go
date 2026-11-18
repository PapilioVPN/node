package httpapi

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v3"
)

func (handler Handler) ReadActivity(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadActivity(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(activityResponse{
		ContextSwitches: result.Activity.ContextSwitches,
		Interrupts:      result.Activity.Interrupts,
		SoftInterrupts:  result.Activity.SoftInterrupts,
		CreatedTasks:    result.Activity.CreatedTasks,
		RunnableTasks:   result.Activity.RunnableTasks,
		IOBlockedTasks:  result.Activity.IOBlockedTasks,
		ObservedAt:      result.ObservedAt, DurationNS: int64(result.Duration),
	})
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
