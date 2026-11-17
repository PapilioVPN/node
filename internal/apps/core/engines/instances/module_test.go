package instances

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go.uber.org/fx"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/settings"
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
	var configuration []settings.Instance
	var bindings []RuntimeBinding
	for _, id := range []domain.InstanceID{"first", "second"} {
		runtime := &runtimeMock{id: id, events: &events, state: domain.Stopped}
		if id == "second" {
			runtime.closeErr = closeErr
		}
		configuration = append(configuration, settings.Instance{ID: id, Engine: "controlled"})
		bindings = append(bindings, RuntimeBinding{Instance: domain.Instance{ID: id, Kind: "controlled"}, Runtime: runtime, Closer: runtime})
	}
	// Group contributions are deliberately reversed; configuration owns ordering.
	slices.Reverse(bindings)
	var resources *Resources
	application := fx.New(fx.NopLogger, fx.Supply(configuration), Module(false), fx.Populate(&resources),
		fx.Provide(fx.Annotate(func() []RuntimeBinding { return bindings }, fx.ResultTags(`group:"engine_runtimes,flatten"`))),
	)
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

func TestInvalidRuntimeContributionsFailBeforeExecution(t *testing.T) {
	var events []string
	runtime := &runtimeMock{id: "first", events: &events}
	first := RuntimeBinding{Instance: domain.Instance{ID: "first", Kind: "controlled"}, Runtime: runtime, Closer: runtime}
	second := first
	second.Instance.ID = "second"
	wrongKind := second
	wrongKind.Instance.Kind = "another"
	missingRuntime := second
	missingRuntime.Runtime = nil
	missingCloser := second
	missingCloser.Closer = nil
	unknown := second
	unknown.Instance.ID = "unknown"
	configuration := []settings.Instance{{ID: "first", Engine: "controlled"}, {ID: "second", Engine: "controlled"}}
	for _, values := range [][]RuntimeBinding{
		{first}, {first, first}, {first, wrongKind}, {first, missingRuntime}, {first, missingCloser}, {first, unknown},
	} {
		if _, err := newResources(configuration, runtimeBindings{Values: values}); err == nil {
			t.Fatal("invalid runtime contributions accepted")
		}
	}
	if len(events) != 0 {
		t.Fatalf("invalid composition executed a runtime: %v", events)
	}
}
