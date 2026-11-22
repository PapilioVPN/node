//go:build linux

package state

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"testing"
)

func TestReviewedPublicationAndPreviousSourcesSurviveReopen(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	store, _ := New(directory, "a")
	if err := store.Open(ctx); err != nil {
		t.Fatal(err)
	}
	previous := domain.Desired{Sources: []domain.Source{{Name: "initial.json", Content: []byte(`{"outbounds":[]}`)}}}
	candidate := []domain.Source{{Name: "candidate.json", Content: []byte(`{"log":{"loglevel":"none"}}`)}}
	receipt := domain.Application{ID: "01234567890123456789012345678901", InstanceID: "a", Outcome: "in_progress", Revision: 1}
	published, err := store.WriteReviewed(ctx, 0, candidate, previous, receipt)
	if err != nil || !published.Durable || published.Metadata.Revision != 1 {
		t.Fatal(published, err)
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	store, _ = New(directory, "a")
	if err := store.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	saved, err := store.ReadApplication(ctx)
	if err != nil || saved.Application.ID != receipt.ID || len(saved.Previous.Sources) != 1 || saved.Previous.Sources[0].Name != "initial.json" {
		t.Fatal(saved, err)
	}
	saved.Application.Outcome = "failed"
	if err := store.SaveApplication(ctx, 1, receipt.ID, saved.Application); err != nil {
		t.Fatal(err)
	}
	got, err := store.ReadApplication(ctx)
	if err != nil || got.Application.Outcome != "failed" {
		t.Fatal(got, err)
	}
	if err := store.SaveApplication(ctx, 0, receipt.ID, saved.Application); err != domain.ErrConflict {
		t.Fatal(err)
	}
	if _, err := store.Write(ctx, 1, candidate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadApplication(ctx); err != domain.ErrNoApplication {
		t.Fatal("legacy publication must supersede old recovery evidence", err)
	}
}

func TestRecoveryAdvancesRevisionAndRetainsReviewedPrevious(t *testing.T) {
	ctx := context.Background()
	store, _ := New(t.TempDir(), "a")
	if err := store.Open(ctx); err != nil {
		t.Fatal(err)
	}
	defer store.Close(ctx)
	original := []domain.Source{{Name: "old.json", Content: []byte(`{}`)}}
	current := []domain.Source{{Name: "bad.json", Content: []byte(`{"log":{"loglevel":"none"}}`)}}
	if _, err := store.Write(ctx, 0, original); err != nil {
		t.Fatal(err)
	}
	previous, _ := store.Read(ctx)
	id := "01234567890123456789012345678901"
	receipt := domain.Application{ID: id, InstanceID: "a", Outcome: "failed", RequiresRecovery: true}
	if _, err := store.WriteReviewed(ctx, 1, current, previous, receipt); err != nil {
		t.Fatal(err)
	}
	recovery := "11234567890123456789012345678901"
	saved, published, err := store.WriteRecovery(ctx, 2, id, recovery)
	if err != nil || !published.Durable || published.Metadata.Revision != 3 || saved.Application.Recovery.ID != recovery {
		t.Fatal(saved, published, err)
	}
	restored, err := store.Read(ctx)
	if err != nil || restored.Metadata.Revision != 3 || string(restored.Sources[0].Content) != "{}" {
		t.Fatal(restored, err)
	}
	if _, _, err := store.WriteRecovery(ctx, 3, id, recovery); err != domain.ErrConflict {
		t.Fatal("recovery was replayed", err)
	}
	if _, _, err := store.WriteRecovery(ctx, 2, id, recovery); err != domain.ErrConflict {
		t.Fatal("stale recovery was accepted", err)
	}
}
