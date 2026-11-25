package events

import (
	"errors"
	"testing"
)

func TestReplayAndSlowSubscriber(t *testing.T) {
	service := New("wg-test")
	for range historyLimit + 1 {
		service.Publish("peers_applied")
	}
	sub, err := service.Subscribe(t.Context(), "wg-test")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	first, err := sub.Next()
	if err != nil || first.Sequence != 2 {
		t.Fatalf("retained event = %v, %v", first, err)
	}
	for range 2*historyLimit + 1 {
		service.Publish("peers_applied")
	}
	if _, err := sub.Next(); err == nil {
		t.Fatal("slow subscriber remained active")
	}
	for range subscriberLimit {
		if _, err := service.Subscribe(t.Context(), "wg-test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Subscribe(t.Context(), "wg-test"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("subscriber limit = %v", err)
	}
	_ = service.Close(t.Context())
}
