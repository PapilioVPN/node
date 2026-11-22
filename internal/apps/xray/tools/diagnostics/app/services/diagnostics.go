// Package services coordinates a bounded, read-only diagnostic report.
package services

import (
	"context"
	"errors"
	"os"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

type StatusReader interface {
	Status(context.Context, string) (instances.Status, error)
}
type MetadataReader interface {
	Metadata(context.Context, string) (configs.DesiredMetadata, error)
}
type BinaryReader interface {
	Read(context.Context) (domain.Binary, error)
}
type NativeReader interface {
	Read(context.Context) (domain.Native, error)
}
type Binding struct {
	ID                   string
	Binary               BinaryReader
	Native               NativeReader
	APIConfigured        bool
	ConfigurationManaged bool
}
type entry struct {
	Binding
	slot chan struct{}
}
type Service struct {
	entries  map[string]entry
	status   StatusReader
	metadata MetadataReader
	slots    chan struct{}
	now      func() time.Time
}

func New(bindings []Binding, status StatusReader, metadata MetadataReader) (*Service, error) {
	if status == nil {
		return nil, domain.ErrInvalidArgument
	}
	s := &Service{entries: map[string]entry{}, status: status, metadata: metadata, slots: make(chan struct{}, 4), now: time.Now}
	for _, b := range bindings {
		if instancevalidators.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.entries[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.entries[b.ID] = entry{Binding: b, slot: make(chan struct{}, 1)}
	}
	return s, nil
}

// Report admits four whole operations globally and one per instance. It reads
// metadata only, one fixed version command and at most two native operations.
// Operational failures form per-source findings; cancellation/selection/overload
// fails the envelope. No cached, desired or declared facts become live health.
func (s *Service) Report(ctx context.Context, request domain.Request) (domain.Report, error) {
	if err := ctx.Err(); err != nil {
		return domain.Report{}, err
	}
	if instancevalidators.ID(request.InstanceID) != nil || request.MaxAgeSeconds > 86400 {
		return domain.Report{}, domain.ErrInvalidArgument
	}
	e, ok := s.entries[request.InstanceID]
	if !ok {
		return domain.Report{}, domain.ErrNotFound
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return domain.Report{}, domain.ErrCapacity
	}
	defer func() { <-s.slots }()
	select {
	case e.slot <- struct{}{}:
	default:
		return domain.Report{}, domain.ErrCapacity
	}
	defer func() { <-e.slot }()
	start := time.Now()
	age := request.MaxAgeSeconds
	if age == 0 {
		age = 60
	}
	r := domain.Report{InstanceID: request.InstanceID, MaxAgeSeconds: age, Consistency: "independent_observations"}
	before, statusErr := s.status.Status(ctx, request.InstanceID)
	r.Execution = execution(before, statusErr)
	if e.Binary == nil {
		r.Binary.Evidence = disabled("xray.executable.version", "inspection_not_configured")
	} else {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		value, err := e.Binary.Read(probe)
		cancel()
		if err != nil {
			r.Binary.Evidence = failure("xray.executable.version", err)
		} else {
			r.Binary = value
		}
	}
	if err := ctx.Err(); err != nil {
		return domain.Report{}, err
	}
	if !e.ConfigurationManaged {
		r.Configuration.Evidence = disabled("node.configs.metadata", "desired_state_not_configured")
	} else if s.metadata == nil {
		r.Configuration.Evidence = failure("node.configs.metadata", domain.ErrUnavailable)
		r.Configuration.Reason = "source_not_bound"
	} else {
		check, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		value, err := s.metadata.Metadata(check, request.InstanceID)
		cancel()
		r.Configuration.Evidence = sample("node.configs.metadata", s.now())
		switch {
		case errors.Is(err, configs.ErrUnsupported):
			r.Configuration.Evidence = disabled("node.configs.metadata", "desired_state_not_configured")
		case err != nil:
			r.Configuration.Evidence = failure("node.configs.metadata", err)
		case value.Revision == 0:
			r.Configuration.State = "absent"
			r.Configuration.Reason = "no_desired_configuration"
		default:
			revision, durable, updated := value.Revision, value.Durable, value.UpdatedAt
			r.Configuration.DesiredRevision = &revision
			r.Configuration.Durable = &durable
			r.Configuration.UpdatedAt = &updated
		}
	}
	if err := ctx.Err(); err != nil {
		return domain.Report{}, err
	}
	switch {
	case !e.APIConfigured:
		r.Native.Evidence = disabled("xray.native_api", "api_not_configured")
	case statusErr != nil:
		r.Native.Evidence = failure("xray.native_api", statusErr)
	case before.Execution.State != instances.Running:
		r.Native.Evidence = failure("xray.native_api", instances.ErrClosed)
		r.Native.Reason = "engine_not_running"
	case e.Native == nil:
		r.Native.Evidence = failure("xray.native_api", domain.ErrUnavailable)
		r.Native.Reason = "source_not_bound"
	default:
		value, err := e.Native.Read(ctx)
		if err != nil {
			r.Native.Evidence = failure("xray.native_api", err)
		} else {
			r.Native = value
			g := before.Execution.Generation
			r.Native.Generation = &g
		}
	}
	after, err := s.status.Status(ctx, request.InstanceID)
	if err != nil {
		r.Consistency = "status_unavailable"
		r.Execution = execution(after, err)
		r.Native.Freshness = "unknown"
	} else {
		r.Execution = execution(after, nil)
		if statusErr != nil || before.Execution.Generation != after.Execution.Generation || before.Execution.State != after.Execution.State {
			r.Consistency = "execution_changed"
		}
	}
	if r.Configuration.DesiredRevision != nil && r.Execution.AppliedRevision != nil && r.Consistency == "independent_observations" {
		equal := *r.Configuration.DesiredRevision == *r.Execution.AppliedRevision
		r.Configuration.MatchesApplied = &equal
	}
	if err := ctx.Err(); err != nil {
		return domain.Report{}, err
	}
	r.Binary.Scope = "configured_executable"
	r.ObservedAt = s.now().UTC()
	r.Duration = time.Since(start)
	for _, finding := range []*domain.Evidence{&r.Execution.Evidence, &r.Configuration.Evidence, &r.Binary.Evidence, &r.Native.Evidence, &r.Native.Runtime, &r.Native.Discovery} {
		freshness(finding, r.ObservedAt, age)
	}
	if r.Native.State != "available" {
		if r.Native.Runtime.State == "" {
			r.Native.Runtime = domain.Evidence{State: "unavailable", Reason: "not_checked", Source: "xray.stats.get_sys_stats", Freshness: "unknown"}
		}
		if r.Native.Discovery.State == "" {
			r.Native.Discovery = domain.Evidence{State: "unavailable", Reason: "not_checked", Source: "grpc.reflection.v1.list_services", Freshness: "unknown"}
		}
	}
	if r.Execution.Freshness != "fresh" {
		r.Configuration.MatchesApplied = nil
	}
	if r.Native.Generation != nil && r.Execution.Freshness != "fresh" {
		r.Consistency = "execution_unverified"
		r.Configuration.MatchesApplied = nil
	}
	if r.Native.Generation != nil && r.Consistency != "independent_observations" {
		r.Native.Reason = r.Consistency
		for _, finding := range []*domain.Evidence{&r.Native.Evidence, &r.Native.Runtime, &r.Native.Discovery} {
			if finding.State == "available" {
				finding.Freshness = "stale"
			}
		}
	}
	if r.Native.Capabilities == nil {
		r.Native.Capabilities = []domain.Capability{}
	}
	return r, nil
}
func execution(s instances.Status, err error) domain.Execution {
	if err != nil {
		return domain.Execution{Evidence: failure("node.instances.status", err)}
	}
	switch s.Execution.State {
	case instances.Running, instances.Stopped, instances.Stopping, instances.Exited:
	default:
		return domain.Execution{Evidence: failure("node.instances.status", domain.ErrMalformed)}
	}
	v := s.Execution
	result := domain.Execution{Evidence: sample("node.instances.status", s.ObservedAt), State: string(v.State), Generation: v.Generation, AppliedRevision: v.ConfigurationRevision, StartedAt: v.StartedAt, ExitedAt: v.ExitedAt, ExitCode: v.ExitCode}
	if v.Failure != nil {
		result.FailureReason = "execution_failed"
	}
	if v.CleanupFailure != nil {
		result.CleanupReason = "cleanup_failed"
	}
	return result
}
func sample(source string, at time.Time) domain.Evidence {
	t := at.UTC()
	return domain.Evidence{State: "available", Source: source, ObservedAt: &t}
}
func disabled(source, reason string) domain.Evidence {
	return domain.Evidence{State: "disabled", Reason: reason, Source: source, Freshness: "unknown"}
}
func failure(source string, err error) domain.Evidence {
	e := domain.Evidence{State: "unavailable", Source: source, Reason: "source_unavailable", Cause: err}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		e.Reason = "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		e.Reason = "canceled"
	case errors.Is(err, domain.ErrCapacity), errors.Is(err, configs.ErrCapacity):
		e.Reason = "resource_exhausted"
	case errors.Is(err, configs.ErrClosed), errors.Is(err, instances.ErrClosed):
		e.Reason = "closed"
	case errors.Is(err, configs.ErrDurabilityUnknown):
		e.Reason = "durability_unknown"
	case errors.Is(err, domain.ErrMalformed):
		e.Reason = "invalid_response"
	case errors.Is(err, os.ErrNotExist):
		e.Reason = "source_not_found"
	case errors.Is(err, os.ErrPermission):
		e.Reason = "permission_denied"
	case errors.Is(err, domain.ErrUnsupported):
		e.State = "unsupported"
		e.Reason = "unsupported"
	}
	return e
}
func freshness(e *domain.Evidence, now time.Time, age uint32) {
	e.Freshness = "unknown"
	if e.State != "available" && e.State != "absent" {
		return
	}
	if e.ObservedAt == nil || e.ObservedAt.IsZero() {
		if e.Reason == "" {
			e.Reason = "observation_time_missing"
		}
		return
	}
	if e.ObservedAt.After(now) {
		if e.Reason == "" {
			e.Reason = "observation_time_in_future"
		}
		return
	}
	e.Freshness = "fresh"
	if now.Sub(*e.ObservedAt) > time.Duration(age)*time.Second {
		e.Freshness = "stale"
		if e.Reason == "" {
			e.Reason = "observation_too_old"
		}
	}
}
