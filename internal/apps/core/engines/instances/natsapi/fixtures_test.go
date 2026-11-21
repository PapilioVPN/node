package natsapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/app/services"
	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

type runtimeFixture struct {
	state domain.Execution
	fail  bool
	calls int
}

func (r *runtimeFixture) Start(ctx context.Context) error {
	r.calls++
	if r.fail {
		return errors.Join(domain.ErrNotApplied, errors.New("private /secret/config failure"))
	}
	r.state.State = domain.Running
	return ctx.Err()
}
func (r *runtimeFixture) Stop(context.Context) error {
	r.calls++
	r.state.State = domain.Stopped
	return nil
}
func (r *runtimeFixture) Observe() domain.Execution           { return r.state }
func (r *runtimeFixture) Check(context.Context) (bool, error) { return true, nil }
func (r *runtimeFixture) APIConfigured() bool                 { return true }
func (r *runtimeFixture) Close(context.Context) error         { return nil }
func fixture() (Handler, *runtimeFixture) {
	r := &runtimeFixture{state: domain.Execution{State: domain.Running, Generation: 9007199254740993}}
	s, _ := services.New([]services.Binding{{ID: "a", Engine: "xray", Runtime: r}})
	return Handler{Service: s, Timeout: time.Second}, r
}
