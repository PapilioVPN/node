package services

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestReplacePreflightPrecedesPersistenceAndExecution(t *testing.T) {
	r := &repositoryFixture{metadata: domain.DesiredMetadata{Revision: 7, Durable: true}}
	var checks, restarts int
	s := stateFixture(t, r, func(context.Context, string, []domain.Source) (domain.Validation, error) {
		checks++
		return domain.Validation{}, nil
	}, func(context.Context, string) (instances.Control, error) { restarts++; return instances.Control{}, nil })
	for _, test := range []struct {
		id       string
		revision uint64
		want     error
	}{{"missing", 7, domain.ErrNotFound}, {"", 7, domain.ErrInvalidArgument}, {"disabled", 7, domain.ErrUnsupported}, {"a", 6, domain.ErrConflict}, {"a", 7, domain.ErrRejected}} {
		_, err := s.Replace(context.Background(), test.id, test.revision, []domain.Source{{Name: "base.json", Content: []byte(`bad`)}})
		if !errors.Is(err, test.want) {
			t.Fatalf("%s: %v", test.id, err)
		}
	}
	if checks != 1 || r.writes != 0 || restarts != 0 {
		t.Fatalf("checks=%d writes=%d restarts=%d", checks, r.writes, restarts)
	}
}
func TestPersistedRevisionSurvivesFailedExecutionAndExplicitReapply(t *testing.T) {
	r := &repositoryFixture{}
	checks, restarts := 0, 0
	nativeCause := errors.New("private native spawn detail")
	s := stateFixture(t, r, func(context.Context, string, []domain.Source) (domain.Validation, error) {
		checks++
		return domain.Validation{Valid: true}, nil
	}, func(context.Context, string) (instances.Control, error) {
		restarts++
		if r.metadata.Revision != 1 {
			t.Error("execution preceded commit")
		}
		return instances.Control{Effect: instances.Partial}, nativeCause
	})
	change, err := s.Replace(context.Background(), "a", 0, []domain.Source{{Name: "base.json", Content: []byte(`{}`)}})
	if !errors.Is(err, nativeCause) || !change.Persistence.Published || !change.Persistence.Durable || change.Execution == nil || change.Execution.Effect != instances.Partial {
		t.Fatalf("%+v %v", change, err)
	}
	s.restarter = restartFunc(func(context.Context, string) (instances.Control, error) {
		restarts++
		return instances.Control{Effect: instances.Completed}, nil
	})
	change, err = s.Apply(context.Background(), "a", 1)
	if err != nil || change.Persistence.Published || !change.Persistence.Durable || change.Persistence.Metadata.Revision != 1 || change.Execution.Effect != instances.Completed || r.writes != 1 || r.confirms != 1 || checks != 1 || restarts != 2 {
		t.Fatalf("%+v %v writes=%d checks=%d restarts=%d", change, err, r.writes, checks, restarts)
	}
	if _, err := s.Apply(context.Background(), "a", 0); !errors.Is(err, domain.ErrNoDesired) {
		t.Fatal(err)
	}
}
func TestUnknownOrNonDurablePublicationNeverStartsExecution(t *testing.T) {
	for _, test := range []struct {
		name        string
		publication domain.Persistence
		err         error
	}{
		{"lost-ack", domain.Persistence{PublicationUnknown: true}, domain.ErrDurabilityUnknown},
		{"directory-sync", domain.Persistence{Published: true, Metadata: domain.DesiredMetadata{Revision: 1}}, domain.ErrDurabilityUnknown},
		{"unconfirmed", domain.Persistence{Published: true}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := &repositoryFixture{write: func(context.Context) (domain.Persistence, error) { return test.publication, test.err }}
			s := stateFixture(t, r, func(context.Context, string, []domain.Source) (domain.Validation, error) {
				return domain.Validation{Valid: true}, nil
			}, func(context.Context, string) (instances.Control, error) {
				t.Error("unconfirmed publication was executed")
				return instances.Control{}, nil
			})
			change, err := s.Replace(context.Background(), "a", 0, nil)
			if !errors.Is(err, domain.ErrDurabilityUnknown) || change.Persistence != test.publication || change.Execution != nil {
				t.Fatalf("%+v %v", change, err)
			}
		})
	}
}
func TestCancellationAfterCommitKeepsPersistenceEvidence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := &repositoryFixture{write: func(context.Context) (domain.Persistence, error) {
		cancel()
		return domain.Persistence{Published: true, Durable: true, Metadata: domain.DesiredMetadata{Revision: 1, Durable: true}}, nil
	}}
	s := stateFixture(t, r, func(context.Context, string, []domain.Source) (domain.Validation, error) {
		return domain.Validation{Valid: true}, nil
	}, func(context.Context, string) (instances.Control, error) {
		t.Error("canceled change restarted")
		return instances.Control{}, nil
	})
	change, err := s.Replace(ctx, "a", 0, nil)
	if !errors.Is(err, context.Canceled) || !change.Persistence.Published || !change.Persistence.Durable || change.Execution != nil {
		t.Fatalf("%+v %v", change, err)
	}
}
func TestStateAdmissionReadsAndShutdown(t *testing.T) {
	entered := make(chan struct{}, 2)
	exited := make(chan error, 2)
	var checks atomic.Int32
	r := &repositoryFixture{}
	s := stateFixture(t, r, func(ctx context.Context, _ string, _ []domain.Source) (domain.Validation, error) {
		checks.Add(1)
		entered <- struct{}{}
		<-ctx.Done()
		return domain.Validation{}, ctx.Err()
	}, func(context.Context, string) (instances.Control, error) {
		t.Error("canceled validation executed")
		return instances.Control{}, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, err := s.Replace(ctx, "a", 0, nil); exited <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := s.Replace(ctx, "a", 0, nil); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if _, err := s.Metadata(ctx, "a"); err != nil {
		t.Fatal("metadata blocked behind validation", err)
	}
	if _, err := s.Read(ctx, "a"); err != nil {
		t.Fatal("source read blocked behind validation", err)
	}
	go func() { _, err := s.Replace(ctx, "b", 0, nil); exited <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := s.Replace(ctx, "c", 0, nil); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := <-exited; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if checks.Load() != 2 || r.writes != 0 {
		t.Fatal("overload reached native validation")
	}
	if _, err := s.Read(ctx, "a"); !errors.Is(err, domain.ErrClosed) {
		t.Fatal(err)
	}
}
