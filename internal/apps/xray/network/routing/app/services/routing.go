// Package services coordinates routing operations against one observed execution.
package services

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// Backend receives input validated by the owning service operation. Adapters
// validate native replies and compiled engine artifacts at their own boundary.
type Backend interface {
	ListRules(context.Context) ([]domain.Rule, error)
	Balancer(context.Context, string) (domain.Balancer, error)
	Probe(context.Context, domain.ProbeRequest) (domain.Context, domain.NativeResult, error)
	Add(context.Context, []byte, domain.Mode) (domain.NativeResult, error)
	Remove(context.Context, string) (domain.NativeResult, error)
	Override(context.Context, string, string) (domain.NativeResult, error)
}
type Preparer interface {
	Prepare(context.Context, string, []byte) ([]byte, error)
}
type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type Binding struct {
	ID      string
	Backend Backend
}
type entry struct {
	backend   Backend
	mu        sync.Mutex
	readers   int
	active    bool
	uncertain uint64
	last      *domain.Operation
}
type Service struct {
	entries       map[string]*entry
	status        StatusReader
	preparer      Preparer
	reads, writes chan struct{}
	pages         pageStore
}

func New(bindings []Binding, status StatusReader, preparer Preparer) (*Service, error) {
	s := &Service{entries: make(map[string]*entry, len(bindings)), status: status, preparer: preparer, reads: make(chan struct{}, 32), writes: make(chan struct{}, 8)}
	s.pages.items = make(map[string]retained)
	for _, b := range bindings {
		if validators.ID(b.ID) != nil || s.entries[b.ID] != nil || status == nil {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = &entry{backend: b.Backend}
	}
	return s, nil
}
func (s *Service) selected(ctx context.Context, id string) (*entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	e := s.entries[id]
	if e == nil {
		return nil, domain.ErrNotFound
	}
	return e, nil
}
func (s *Service) Status(ctx context.Context, id string) (domain.ControlStatus, error) {
	if err := ctx.Err(); err != nil {
		return domain.ControlStatus{}, err
	}
	if validators.ID(id) != nil {
		return domain.ControlStatus{}, domain.ErrInvalidArgument
	}
	e, err := s.selected(ctx, id)
	if err != nil {
		return domain.ControlStatus{}, err
	}
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.ControlStatus{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	result := domain.ControlStatus{Observation: domain.Observation{InstanceID: id, Generation: state.Execution.Generation, ObservedAt: time.Now().UTC()}, ExecutionState: string(state.Execution.State), APIConfigured: e.backend != nil, RequestInFlight: e.active, RequiresInstanceRestart: e.uncertain != 0 && e.uncertain == state.Execution.Generation, RoutingStatsStream: "disabled_stock_xray"}
	if e.last != nil {
		copy := *e.last
		result.LastOperation = &copy
	}
	return result, nil
}
func (s *Service) begin(ctx context.Context, id string, write bool) (*entry, uint64, func(), error) {
	e, err := s.selected(ctx, id)
	if err != nil {
		return nil, 0, nil, err
	}
	if e.backend == nil {
		return nil, 0, nil, domain.ErrUnsupported
	}
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return nil, 0, nil, err
	}
	generation := state.Execution.Generation
	if state.Execution.State != instances.Running {
		return nil, generation, nil, domain.ErrNotRunning
	}
	slots := s.reads
	if write {
		slots = s.writes
	}
	select {
	case slots <- struct{}{}:
	default:
		return nil, generation, nil, domain.ErrCapacity
	}
	e.mu.Lock()
	var reject error
	switch {
	case e.uncertain != 0 && e.uncertain == generation:
		reject = domain.ErrUncertain
	case e.active || (write && e.readers > 0) || (!write && e.readers >= 8):
		reject = domain.ErrCapacity
	}
	if reject != nil {
		e.mu.Unlock()
		<-slots
		return nil, generation, nil, reject
	}
	if write {
		e.active = true
	} else {
		e.readers++
	}
	e.mu.Unlock()
	done := func() {
		e.mu.Lock()
		if write {
			e.active = false
		} else {
			e.readers--
		}
		e.mu.Unlock()
		<-slots
	}
	if err := ctx.Err(); err != nil {
		done()
		return nil, generation, nil, err
	}
	return e, generation, done, nil
}
func (s *Service) stable(ctx context.Context, id string, generation uint64) error {
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return err
	}
	if state.Execution.Generation != generation || state.Execution.State != instances.Running {
		return domain.ErrExecutionChanged
	}
	return nil
}
func (s *Service) mutate(ctx context.Context, id, kind string, call func(Backend, uint64) (domain.NativeResult, error)) (o domain.Operation, err error) {
	o = domain.Operation{InstanceID: id, Kind: kind, Effect: domain.NotApplied}
	e, generation, done, err := s.begin(ctx, id, true)
	o.Generation = generation
	if err != nil {
		o.RequiresInstanceRestart = errors.Is(err, domain.ErrUncertain)
		return o, err
	}
	defer done()
	started := time.Now()
	native, nativeErr := call(e.backend, generation)
	o.Effect = native.Effect
	if o.Effect == "" {
		o.Effect = domain.Unknown
	}
	o.Acknowledged = native.Acknowledged
	o.RequiresInstanceRestart = native.RequiresInstanceRestart
	// Status is a local observation. Preserve acknowledged success when caller
	// cancellation races after the native response; never turn it into unsent work.
	statusErr := s.stable(context.WithoutCancel(ctx), id, generation)
	if statusErr != nil && o.Effect != domain.NotApplied {
		o.Effect = domain.Unknown
		o.RequiresInstanceRestart = true
	}
	o.ObservedAt = time.Now().UTC()
	o.Duration = time.Since(started)
	e.mu.Lock()
	copy := o
	copy.InstanceID = strings.Clone(id)
	e.last = &copy
	if o.RequiresInstanceRestart {
		e.uncertain = generation
	}
	e.mu.Unlock()
	return o, errors.Join(nativeErr, statusErr)
}
func (s *Service) Add(ctx context.Context, r domain.AddRequest) (domain.Operation, error) {
	if err := validators.Add(r); err != nil {
		return domain.Operation{InstanceID: r.InstanceID, Kind: "add_rule", Effect: domain.NotApplied}, err
	}
	return s.mutate(ctx, r.InstanceID, "add_rule", func(b Backend, generation uint64) (domain.NativeResult, error) {
		if s.preparer == nil {
			return domain.NativeResult{Effect: domain.NotApplied}, domain.ErrUnsupported
		}
		data, err := s.preparer.Prepare(ctx, r.InstanceID, r.RuleJSON)
		if err != nil {
			return domain.NativeResult{Effect: domain.NotApplied}, err
		}
		if err := ctx.Err(); err != nil {
			return domain.NativeResult{Effect: domain.NotApplied}, err
		}
		// Preparation can take time; reject a known local execution replacement
		// before submitting. This is not native CAS against external writers.
		if err := s.stable(ctx, r.InstanceID, generation); err != nil {
			return domain.NativeResult{Effect: domain.NotApplied}, err
		}
		return b.Add(ctx, data, r.Mode)
	})
}
func (s *Service) Remove(ctx context.Context, id, tag string) (domain.Operation, error) {
	if validators.ID(id) != nil || validators.Tag(tag) != nil {
		return domain.Operation{InstanceID: id, Kind: "remove_rule", Effect: domain.NotApplied}, domain.ErrInvalidArgument
	}
	return s.mutate(ctx, id, "remove_rule", func(b Backend, _ uint64) (domain.NativeResult, error) { return b.Remove(ctx, tag) })
}
func (s *Service) Override(ctx context.Context, r domain.OverrideRequest) (domain.Operation, error) {
	if validators.ID(r.InstanceID) != nil || validators.Tag(r.BalancerTag) != nil || r.Target == nil || !validators.Text(*r.Target) {
		return domain.Operation{InstanceID: r.InstanceID, Kind: "override_balancer", Effect: domain.NotApplied}, domain.ErrInvalidArgument
	}
	return s.mutate(ctx, r.InstanceID, "override_balancer", func(b Backend, _ uint64) (domain.NativeResult, error) {
		return b.Override(ctx, r.BalancerTag, *r.Target)
	})
}
func (s *Service) Probe(ctx context.Context, r domain.ProbeRequest) (result domain.ProbeResult, err error) {
	if err := validators.Probe(r); err != nil {
		return domain.ProbeResult{Operation: domain.Operation{InstanceID: r.InstanceID, Kind: "test_route", Effect: domain.NotApplied}}, err
	}
	result.Operation, err = s.mutate(ctx, r.InstanceID, "test_route", func(b Backend, _ uint64) (domain.NativeResult, error) {
		route, native, err := b.Probe(ctx, r)
		if err == nil {
			result.Route = &route
		}
		return native, err
	})
	if err != nil {
		result.Route = nil
	}
	return result, err
}
func (s *Service) Balancer(ctx context.Context, id, tag string) (domain.BalancerResult, error) {
	if validators.ID(id) != nil || validators.Tag(tag) != nil {
		return domain.BalancerResult{}, domain.ErrInvalidArgument
	}
	e, g, done, err := s.begin(ctx, id, false)
	if err != nil {
		return domain.BalancerResult{}, err
	}
	defer done()
	started := time.Now()
	b, err := e.backend.Balancer(ctx, tag)
	if err != nil {
		return domain.BalancerResult{}, err
	}
	if err := s.stable(ctx, id, g); err != nil {
		return domain.BalancerResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.BalancerResult{}, err
	}
	return domain.BalancerResult{Observation: domain.Observation{InstanceID: id, Generation: g, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}, Tag: tag, Balancer: b}, nil
}
