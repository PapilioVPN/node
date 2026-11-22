package services

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type repositoryFixture struct {
	metadata         domain.DesiredMetadata
	writes, confirms int
	write            func(context.Context) (domain.Persistence, error)
	confirm          func(context.Context) (domain.DesiredMetadata, error)
}

func (r *repositoryFixture) Metadata(context.Context) (domain.DesiredMetadata, error) {
	return r.metadata, nil
}
func (r *repositoryFixture) Read(context.Context) (domain.Desired, error) {
	return domain.Desired{Metadata: r.metadata}, nil
}
func (r *repositoryFixture) Write(ctx context.Context, expected uint64, sources []domain.Source) (domain.Persistence, error) {
	r.writes++
	if r.write != nil {
		return r.write(ctx)
	}
	r.metadata = domain.DesiredMetadata{Revision: expected + 1, SourceCount: len(sources), Durable: true}
	return domain.Persistence{Metadata: r.metadata, Published: true, Durable: true}, nil
}
func (r *repositoryFixture) Confirm(ctx context.Context, expected uint64) (domain.DesiredMetadata, error) {
	r.confirms++
	if r.confirm != nil {
		return r.confirm(ctx)
	}
	if expected != r.metadata.Revision {
		return r.metadata, domain.ErrConflict
	}
	return r.metadata, nil
}

type sourceCheckFunc func(context.Context, string, []domain.Source) (domain.Validation, error)

func (f sourceCheckFunc) CheckSources(ctx context.Context, id string, sources []domain.Source) (domain.Validation, error) {
	return f(ctx, id, sources)
}

type restartFunc func(context.Context, string) (instances.Control, error)

func (f restartFunc) Restart(ctx context.Context, id string) (instances.Control, error) {
	return f(ctx, id)
}
func (restartFunc) Status(context.Context, string) (instances.Status, error) {
	panic("legacy changes must not acquire planned status")
}
func (restartFunc) Check(context.Context, string) (instances.Readiness, error) {
	panic("legacy changes must not acquire planned readiness")
}
func stateFixture(t *testing.T, r *repositoryFixture, checker sourceCheckFunc, restart restartFunc) *StateService {
	t.Helper()
	s, err := NewState([]StateBinding{{ID: "a", Repository: r}, {ID: "b", Repository: r}, {ID: "c", Repository: r}, {ID: "disabled"}}, checker, restart)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return s
}
