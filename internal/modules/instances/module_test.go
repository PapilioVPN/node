package instances

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/modules/instances/app"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// runtimeMock records ownership transitions without native engine execution.
type runtimeMock struct {
	id       domain.InstanceID
	events   *[]string
	closeErr error
	state    domain.State
}

func (m *runtimeMock) Start(context.Context) error {
	*m.events = append(*m.events, "start "+string(m.id))
	m.state = domain.Running
	return nil
}
func (m *runtimeMock) Stop(context.Context) error {
	*m.events = append(*m.events, "stop "+string(m.id))
	m.state = domain.Stopped
	return nil
}
func (m *runtimeMock) Observe() (domain.State, error) { return m.state, nil }
func (m *runtimeMock) Close() error {
	*m.events = append(*m.events, "close "+string(m.id))
	return m.closeErr
}

func TestResourceOwnershipWithIndependentRuntimes(t *testing.T) {
	var events []string
	closeErr := errors.New("controlled close failure")
	resources := &Resources{}
	for _, id := range []domain.InstanceID{"first", "second"} {
		runtime := &runtimeMock{id: id, events: &events, state: domain.Stopped}
		if id == "second" {
			runtime.closeErr = closeErr
		}
		service := app.NewLifecycle(domain.Instance{ID: id, Kind: "controlled"}, runtime)
		resources.instances = append(resources.instances, ownedInstance{id: id, lifecycle: service, closer: runtime})
	}
	application := fx.New(fx.NopLogger, fx.Supply(resources), fx.Invoke(registerLifecycle))
	if err := application.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := newLifecycleService(resources)
	observed, err := service.GetInstanceStatus(context.Background(), "second")
	if err != nil || observed.Instance.ID != "second" || observed.State != domain.Running {
		t.Fatalf("wrong bound runtime: %+v, %v", observed, err)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resources.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("lost native cleanup cause: %v", err)
	}
	want := []string{"start first", "start second", "stop second", "stop first", "close second", "close first"}
	if !slices.Equal(events, want) {
		t.Fatalf("ownership ordering: %v; want %v", events, want)
	}
}
