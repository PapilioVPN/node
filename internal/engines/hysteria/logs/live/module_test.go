package live

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestCapacityAndExecutionEnd(t *testing.T) {
	ex, cancel := context.WithCancel(t.Context())
	defer cancel()
	j := NewJournal(ex, 1)
	s := New(func(context.Context, string) (*Journal, error) { return j, nil })
	defer s.Close()
	slow, err := s.Subscribe(t.Context(), "edge")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	fast, err := s.Subscribe(t.Context(), "edge")
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	for range maxSubscriptions - 2 {
		sub, err := s.Subscribe(t.Context(), "edge")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(sub.Close)
	}
	if _, err := s.Subscribe(t.Context(), "edge"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	for n := 1; n <= queueCapacity+2; n++ {
		j.Publish("client_connected")
		got, err := fast.Next(t.Context())
		if err != nil || got.Sequence != uint64(n) {
			t.Fatalf("fast subscriber sequence %d: %v", got.Sequence, err)
		}
	}
	for n := 1; n <= queueCapacity; n++ {
		got, err := slow.Next(t.Context())
		if err != nil || got.Sequence != uint64(n) {
			t.Fatalf("slow subscriber sequence %d: %v", got.Sequence, err)
		}
	}
	j.Publish("client_disconnected")
	got, err := slow.Next(t.Context())
	if err != nil || got.Sequence != queueCapacity+3 {
		t.Fatalf("sequence gap: %+v %v", got, err)
	}

	s.Close()
	wait, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if _, err := slow.Next(wait); err != io.EOF {
		t.Fatalf("close did not end stream: %v", err)
	}
	if ex.Err() != nil {
		t.Fatal("closing management logs stopped the execution")
	}
}
