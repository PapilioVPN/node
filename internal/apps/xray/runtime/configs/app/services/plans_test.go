package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type reviewStore struct {
	current                 domain.Desired
	saved                   domain.StoredApplication
	writes, saves, recovers int
}

func (r *reviewStore) Metadata(context.Context) (domain.DesiredMetadata, error) {
	return r.current.Metadata, nil
}
func (r *reviewStore) Read(context.Context) (domain.Desired, error) { return r.current, nil }
func (r *reviewStore) Write(ctx context.Context, expected uint64, sources []domain.Source) (domain.Persistence, error) {
	return r.WriteReviewed(ctx, expected, sources, r.current, domain.Application{})
}
func (r *reviewStore) Confirm(context.Context, uint64) (domain.DesiredMetadata, error) {
	return r.current.Metadata, nil
}
func (r *reviewStore) WriteReviewed(_ context.Context, expected uint64, sources []domain.Source, previous domain.Desired, app domain.Application) (domain.Persistence, error) {
	r.writes++
	if expected != r.current.Metadata.Revision {
		return domain.Persistence{}, domain.ErrConflict
	}
	r.current = domain.Desired{Metadata: domain.DesiredMetadata{Revision: expected + 1, SHA256: policies.SourceDigest(sources), Durable: true}, Sources: ownSources(sources)}
	app.Revision = expected + 1
	app.ReceiptDurable = true
	r.saved = domain.StoredApplication{Application: app, Previous: previous}
	return domain.Persistence{Metadata: r.current.Metadata, Durable: true, Published: true}, nil
}
func (r *reviewStore) ReadApplication(context.Context) (domain.StoredApplication, error) {
	if r.saved.Application.ID == "" {
		return domain.StoredApplication{}, domain.ErrNoApplication
	}
	return r.saved, nil
}
func (r *reviewStore) SaveApplication(_ context.Context, expected uint64, _ string, app domain.Application) error {
	r.saves++
	if expected != r.current.Metadata.Revision {
		return domain.ErrConflict
	}
	app.ReceiptDurable = true
	r.saved.Application = cloneApplication(app)
	return nil
}
func (r *reviewStore) WriteRecovery(_ context.Context, expected uint64, _ string, id string) (domain.StoredApplication, domain.Persistence, error) {
	if expected != r.current.Metadata.Revision {
		return domain.StoredApplication{}, domain.Persistence{}, domain.ErrConflict
	}
	r.recovers++
	r.current = domain.Desired{Metadata: domain.DesiredMetadata{Revision: expected + 1, SHA256: policies.SourceDigest(r.saved.Previous.Sources), Durable: true}, Sources: r.saved.Previous.Sources}
	r.saved.Application.Recovery = &domain.Recovery{ID: id, ExpectedRevision: expected, Revision: expected + 1}
	return r.saved, domain.Persistence{Metadata: r.current.Metadata, Durable: true, Published: true}, nil
}

type reviewRuntime struct {
	store            *reviewStore
	state            instances.Status
	restarts, checks int
	unready          bool
}

func (r *reviewRuntime) Status(context.Context, string) (instances.Status, error) {
	return r.state, nil
}
func (r *reviewRuntime) Restart(context.Context, string) (instances.Control, error) {
	r.restarts++
	r.state.Execution.Generation++
	r.state.Execution.ExecutionID = fmt.Sprint("execution-", r.state.Execution.Generation)
	v := r.store.current.Metadata.Revision
	r.state.Execution.ConfigurationRevision = &v
	copy := r.state
	return instances.Control{Effect: instances.Completed, Status: &copy}, nil
}
func (r *reviewRuntime) Check(context.Context, string) (instances.Readiness, error) {
	r.checks++
	state := instances.ProbeReady
	if r.unready {
		state = instances.ProbeNotConfigured
	}
	return instances.Readiness{Status: r.state, APIState: state}, nil
}

type reviewCompiler struct{ calls int }

func (c *reviewCompiler) Compile(_ context.Context, _ string, sources []domain.Source) (domain.Compilation, error) {
	c.calls++
	return domain.Compilation{Content: bytes.Clone(sources[0].Content)}, nil
}

type reviewComparison struct{}

func (reviewComparison) Compare(_ context.Context, _, _ []domain.Source, before, after []byte) (domain.Assessment, error) {
	mode := domain.Restart
	if string(before) == string(after) {
		mode = domain.Unchanged
	} else if string(after) == "live" {
		mode = domain.LiveRouting
	}
	return domain.Assessment{Mode: mode, Payload: after}, nil
}

type reviewLive struct {
	calls int
	fail  bool
}

func (l *reviewLive) ApplyRouting(context.Context, []byte) (domain.LiveResult, error) {
	l.calls++
	if l.fail {
		return domain.LiveResult{Effect: "unknown"}, errors.New("private native detail")
	}
	return domain.LiveResult{Effect: "completed", Acknowledged: true}, nil
}
func setupReview(t *testing.T) (*Plans, *reviewStore, *reviewRuntime, *reviewCompiler, *reviewLive) {
	t.Helper()
	sources := []domain.Source{{Name: "base.json", Content: []byte("old")}}
	repo := &reviewStore{current: domain.Desired{Metadata: domain.DesiredMetadata{Revision: 1, SHA256: policies.SourceDigest(sources), Durable: true}, Sources: sources}}
	rev := uint64(1)
	runtime := &reviewRuntime{store: repo, state: instances.Status{ID: "a", Execution: instances.Execution{State: instances.Running, Generation: 1, ExecutionID: "execution-1", ConfigurationRevision: &rev}}}
	state, _ := NewState([]StateBinding{{ID: "a", Repository: repo}}, nil, runtime)
	t.Cleanup(func() { state.Close(context.Background()) })
	compiler, live := &reviewCompiler{}, &reviewLive{}
	plans, err := NewPlans(state, compiler, reviewComparison{}, runtime, []PlanBinding{{ID: "a", Repository: repo, Live: live}})
	if err != nil {
		t.Fatal(err)
	}
	return plans, repo, runtime, compiler, live
}
func TestReviewedModesRejectStaleAndConsumeEachIdentityOnce(t *testing.T) {
	for _, content := range []string{"old", "live", "restart"} {
		t.Run(content, func(t *testing.T) {
			s, r, run, compiler, live := setupReview(t)
			plan, err := s.Plan(context.Background(), "a", 1, []domain.Source{{Name: "base.json", Content: []byte(content)}})
			if err != nil || r.writes != 0 || run.restarts != 0 || live.calls != 0 {
				t.Fatal(plan, err)
			}
			calls := 2
			if content == "old" {
				calls = 1
			}
			if compiler.calls != calls {
				t.Fatal(compiler.calls)
			}
			result, err := s.Apply(context.Background(), "a", plan.ID)
			if err != nil || result.RequiresRecovery || !result.ReceiptDurable || result.Revision != 2 {
				t.Fatal(result, err)
			}
			restarts, mutations := run.restarts, live.calls
			if content == "old" && (restarts != 0 || mutations != 0 || run.checks != 0 || result.Outcome != "unchanged") {
				t.Fatal(result)
			}
			if content == "live" && (restarts != 0 || mutations != 1 || result.Readiness != "api_ready") {
				t.Fatal(result)
			}
			if content == "restart" && (restarts != 1 || mutations != 0 || result.Readiness != "api_ready") {
				t.Fatal(result)
			}
			again, err := s.Apply(context.Background(), "a", plan.ID)
			if err != nil || again.ID != result.ID || r.writes != 1 || run.restarts != restarts || live.calls != mutations {
				t.Fatal("replayed effects", again, err)
			}
		})
	}
	s, r, run, _, live := setupReview(t)
	plan, _ := s.Plan(context.Background(), "a", 1, []domain.Source{{Name: "base.json", Content: []byte("live")}})
	r.current.Metadata.Revision++
	result, err := s.Apply(context.Background(), "a", plan.ID)
	if !errors.Is(err, domain.ErrStalePlan) || result.Persistence.Published || run.restarts != 0 || live.calls != 0 || r.writes != 0 {
		t.Fatal(result, err)
	}
}
func TestFailedLiveApplicationAndInterruptedReceiptRecoverWithoutReplay(t *testing.T) {
	s, r, run, _, live := setupReview(t)
	plan, _ := s.Plan(context.Background(), "a", 1, []domain.Source{{Name: "base.json", Content: []byte("live")}})
	live.fail = true
	result, err := s.Apply(context.Background(), "a", plan.ID)
	if err == nil || result.Outcome != "unknown" || !result.RequiresRecovery || !result.Persistence.Published || result.Error == "private native detail" {
		t.Fatal(result, err)
	}
	result, err = s.Application(context.Background(), "a", plan.ID)
	if err != nil || result.EngineEffect != "unknown" {
		t.Fatal(result, err)
	}
	// Model reconstruction after a publication whose final receipt was interrupted.
	r.saved.Application.Outcome = "in_progress"
	s.mu.Lock()
	s.entries["a"].last = nil
	s.mu.Unlock()
	interrupted, err := s.Apply(context.Background(), "a", plan.ID)
	if err != nil || interrupted.Outcome != "recovery_required" || interrupted.EngineEffect != "unknown" || live.calls != 1 || run.restarts != 0 {
		t.Fatal(interrupted, err)
	}
	id := identity()
	recovered, err := s.Recover(context.Background(), "a", plan.ID, 2, id)
	if err != nil || recovered.RequiresRecovery || recovered.Recovery.Outcome != "completed" || recovered.Recovery.Revision != 3 || run.restarts != 1 || string(r.current.Sources[0].Content) != "old" {
		t.Fatal(recovered, err)
	}
	if _, err = s.Recover(context.Background(), "a", plan.ID, 2, id); err != nil || run.restarts != 1 || r.recovers != 1 {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.entries["a"].last = nil
	s.mu.Unlock()
	recovered, err = s.Application(context.Background(), "a", plan.ID)
	if err != nil || recovered.RequiresRecovery || recovered.Recovery.Outcome != "completed" {
		t.Fatal("completed recovery became interrupted again", recovered, err)
	}

}

func TestChangedGenerationExpiryAndSupersedingPlansHaveNoEffects(t *testing.T) {
	for _, kind := range []string{"generation", "expired", "superseded"} {
		t.Run(kind, func(t *testing.T) {
			s, r, run, _, live := setupReview(t)
			source := []domain.Source{{Name: "base.json", Content: []byte("live")}}
			plan, _ := s.Plan(context.Background(), "a", 1, source)
			expected := domain.ErrPlanExpired
			switch kind {
			case "generation":
				run.state.Execution.Generation++
				expected = domain.ErrStalePlan
			case "expired":
				s.entries["a"].pending.expires = time.Now().Add(-time.Second)
			case "superseded":
				s.Plan(context.Background(), "a", 1, source)
			}
			_, err := s.Apply(context.Background(), "a", plan.ID)
			if !errors.Is(err, expected) || r.writes != 0 || run.restarts != 0 || live.calls != 0 {
				t.Fatal(err, r.writes, run.restarts, live.calls)
			}
		})
	}
}
func TestReviewedCandidateOwnsCallerBuffers(t *testing.T) {
	s, r, _, _, _ := setupReview(t)
	content := []byte("live")
	plan, err := s.Plan(context.Background(), "a", 1, []domain.Source{{Name: "base.json", Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	clear(content)
	if _, err := s.Apply(context.Background(), "a", plan.ID); err != nil {
		t.Fatal(err)
	}
	if string(r.current.Sources[0].Content) != "live" || r.current.Metadata.SHA256 != plan.CandidateSHA256 {
		t.Fatal("reviewed source borrowed transport memory")
	}
}
