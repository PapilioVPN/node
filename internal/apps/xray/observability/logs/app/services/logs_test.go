package services_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

type source struct {
	mu         sync.Mutex
	data       string
	execution  string
	generation uint64
	capacity   bool
	uncertain  bool
	exited     bool
	calls      int
}

func (s *source) Read(ctx context.Context, execution string, offset uint64, limit uint32) (domain.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if err := ctx.Err(); err != nil {
		return domain.Chunk{}, err
	}
	if execution != "" && execution != s.execution {
		return domain.Chunk{}, domain.ErrExecutionChanged
	}
	if offset > uint64(len(s.data)) {
		return domain.Chunk{}, domain.ErrInvalidArgument
	}
	end := min(offset+uint64(limit), uint64(len(s.data)))
	return domain.Chunk{ExecutionID: s.execution, Generation: s.generation, Offset: offset, NextOffset: end, Data: []byte(s.data[offset:end]), CapacityReached: s.capacity, PositionUncertain: s.uncertain, Exited: s.exited, ObservedAt: time.Now().UTC()}, nil
}
func setup(t *testing.T, text string) (*services.Service, *source) {
	t.Helper()
	source := &source{data: text, execution: "run-a", generation: 1}
	service, err := services.New([]services.Binding{{ID: "edge", Reader: source}, {ID: "disabled"}})
	if err != nil {
		t.Fatal(err)
	}
	return service, source
}
func TestTailAndStableResume(t *testing.T) {
	s, src := setup(t, strings.Repeat("[Info] private secret\n", 51))
	p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
	if err != nil || len(p.Records) != 50 || len(p.Gaps) != 1 || p.Gaps[0].Reason != "tail_omitted" {
		t.Fatal(p, err)
	}
	src.mu.Lock()
	src.data += "[Error] new private key\n"
	src.mu.Unlock()
	next, err := s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || len(next.Records) != 1 || next.Records[0].Level != "error" || next.Records[0].StartOffset != p.Records[49].NextOffset {
		t.Fatal(next, err)
	}
	replay, err := s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || replay.NextCursor != next.NextCursor || len(replay.Records) != 1 {
		t.Fatal(replay, err)
	}
}
func TestPartialOversizeAndChangedExecution(t *testing.T) {
	s, src := setup(t, "partial secret")
	p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
	if err != nil || len(p.Records) != 0 {
		t.Fatal(p, err)
	}
	src.mu.Lock()
	src.data += " complete\n"
	src.mu.Unlock()
	p, err = s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || len(p.Records) != 1 {
		t.Fatal(p, err)
	}
	src.mu.Lock()
	src.execution = "run-b"
	src.generation = 2
	src.data = strings.Repeat("s", 5000) + "\n[Info] short\n"
	src.mu.Unlock()
	p, err = s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || len(p.Gaps) < 2 || p.Gaps[0].Reason != "execution_changed" || p.ExecutionID != "run-b" || len(p.Records) != 2 || !p.Records[0].Truncated {
		t.Fatal(p, err)
	}
}
func TestCancellationAndCapacity(t *testing.T) {
	s, src := setup(t, "")
	p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Follow(ctx, "edge", domain.FollowRequest{Cursor: p.NextCursor, WaitMilliseconds: 1000}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.capacity = true
	src.uncertain = true
	src.mu.Unlock()
	p, err = s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor, WaitMilliseconds: 1000})
	if err != nil || !p.CapacityReached || len(p.Gaps) != 2 {
		t.Fatal(p, err)
	}
}
func TestBoundsAndSelections(t *testing.T) {
	s, _ := setup(t, "")
	if _, err := s.Tail(context.Background(), "edge", domain.TailRequest{Limit: 51}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if _, err := s.Tail(context.Background(), "unknown", domain.TailRequest{}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.Tail(context.Background(), "disabled", domain.TailRequest{}); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: "bad"}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestWorkGrowthAndTinyRecords(t *testing.T) {
	for _, n := range []int{0, 1, 50, 51, 1000, domain.MaxOutputBytes} {
		s, src := setup(t, strings.Repeat("\n", n))
		p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
		if err != nil || len(p.Records) != min(n, 50) || src.calls > 17 {
			t.Fatal(n, len(p.Records), src.calls, err)
		}
		if n > 50 && p.Records[0].StartOffset != uint64(n-50) {
			t.Fatal("incorrect recent record order", p)
		}
	}
}
func TestLongRecordResumeSkipsToDelimiter(t *testing.T) {
	s, src := setup(t, "")
	p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
	if err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.data = strings.Repeat("secret", 20000) + "\n[Error] last\n"
	src.mu.Unlock()
	p, err = s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || len(p.Records) != 1 || !p.Records[0].Truncated {
		t.Fatal(p, err)
	}
	p, err = s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor})
	if err != nil || len(p.Records) != 1 || p.Records[0].Level != "error" {
		t.Fatal(p, err)
	}
}
func TestFollowLeaseExpiresAndIsCanceled(t *testing.T) {
	s, _ := setup(t, "")
	p, err := s.Tail(context.Background(), "edge", domain.TailRequest{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	empty, err := s.Follow(context.Background(), "edge", domain.FollowRequest{Cursor: p.NextCursor, WaitMilliseconds: 20})
	if err != nil || len(empty.Records) != 0 || time.Since(started) < 20*time.Millisecond || time.Since(started) > time.Second {
		t.Fatal(empty, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := s.Follow(ctx, "edge", domain.FollowRequest{Cursor: p.NextCursor, WaitMilliseconds: 1000})
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not join")
	}
}

type blockingSource struct{ entered chan struct{} }

func (s blockingSource) Read(ctx context.Context, _ string, _ uint64, _ uint32) (domain.Chunk, error) {
	s.entered <- struct{}{}
	<-ctx.Done()
	return domain.Chunk{}, ctx.Err()
}
func TestAdmissionIsBoundedAndReleased(t *testing.T) {
	reader := blockingSource{make(chan struct{}, 32)}
	s, err := services.New([]services.Binding{{ID: "edge", Reader: reader}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for range domain.MaxInstanceReads {
		wg.Add(1)
		go func() { defer wg.Done(); s.Tail(ctx, "edge", domain.TailRequest{}) }()
	}
	for range domain.MaxInstanceReads {
		<-reader.entered
	}
	if _, err := s.Tail(context.Background(), "edge", domain.TailRequest{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := s.Tail(ctx, "edge", domain.TailRequest{}); done <- err }()
	<-reader.entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGlobalAdmissionAcrossInstances(t *testing.T) {
	reader := blockingSource{make(chan struct{}, domain.MaxConcurrentReads)}
	bindings := make([]services.Binding, 9)
	for i := range bindings {
		bindings[i] = services.Binding{ID: string(rune('a' + i)), Reader: reader}
	}
	s, err := services.New(bindings)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	for i := range domain.MaxConcurrentReads {
		wg.Add(1)
		go func() { defer wg.Done(); s.Tail(ctx, bindings[i/domain.MaxInstanceReads].ID, domain.TailRequest{}) }()
	}
	for range domain.MaxConcurrentReads {
		<-reader.entered
	}
	if _, err := s.Tail(context.Background(), bindings[8].ID, domain.TailRequest{}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	wg.Wait()
}
