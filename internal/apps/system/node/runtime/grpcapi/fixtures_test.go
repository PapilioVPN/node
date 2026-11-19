package grpcapi_test

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type reader struct {
	calls int
	cause error
}

func (r *reader) Info(context.Context) (domain.Info, error) {
	r.calls++
	modified := false
	return domain.Info{PID: 42, GoVersion: "fixture", Modified: &modified}, r.cause
}
func (r *reader) Resources(context.Context) (domain.Resources, error) {
	r.calls++
	return domain.Resources{PID: 42, StartTicks: 9007199254740993, HeapObjectsBytes: 0}, r.cause
}
func (r *reader) Read(context.Context) (domain.Diagnostics, error) {
	r.calls++
	return domain.Diagnostics{HTTP: &domain.Listener{Ready: true}, NATS: &domain.Broker{State: "CLOSED", ErrorCode: "not_connected", Cause: errors.New("private native cause")}}, r.cause
}
