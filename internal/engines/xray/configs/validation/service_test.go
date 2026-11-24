package validation

import (
	"context"
	"errors"
	"testing"
)

func TestValidationCancelsAndReleasesItsSlot(t *testing.T) {
	service := New(Config{ID: "primary"})
	started := make(chan struct{})
	service.run = func(ctx context.Context, _ Config) (*Issue, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := service.Validate(ctx, Request{ID: "primary"})
		result <- err
	}()
	<-started
	if _, err := service.Validate(t.Context(), Request{ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown instance error = %v", err)
	}
	if _, err := service.Validate(t.Context(), Request{ID: "primary"}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent validation error = %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled validation error = %v", err)
	}
	service.run = func(context.Context, Config) (*Issue, error) { return nil, nil }
	got, err := service.Validate(t.Context(), Request{ID: "primary"})
	if err != nil || got.ID != "primary" || got.Issue != nil {
		t.Fatalf("retry validation = (%+v, %v)", got, err)
	}
}
