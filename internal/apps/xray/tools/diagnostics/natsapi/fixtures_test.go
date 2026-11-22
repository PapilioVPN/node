package natsapi

import (
	"context"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type statusReader struct{ calls int }

func (s *statusReader) Status(context.Context, string) (instances.Status, error) {
	s.calls++
	return instances.Status{Execution: instances.Execution{State: instances.Stopped}, ObservedAt: time.Now().UTC()}, nil
}
