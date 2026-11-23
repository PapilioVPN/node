package live

import (
	"errors"
	"io"
	"testing"
)

func TestSubscriptionsDropOnlyForSlowConsumer(t *testing.T) {
	service := New("primary")
	run := service.Prepare()
	run.Activate()
	fast, err := service.Subscribe("primary")
	if err != nil {
		t.Fatal(err)
	}
	defer fast.Close()
	slow, err := service.Subscribe("primary")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()

	payload := []byte("original")
	for n := 1; n <= QueueCapacity+10; n++ {
		run.Publish(Stdout, payload, false)
		event, err := fast.Next(t.Context())
		if err != nil || event.Sequence != uint64(n) {
			t.Fatalf("fast reader event %d = %+v, %v", n, event, err)
		}
	}
	payload[0] = 'X'
	for n := 1; n <= QueueCapacity; n++ {
		event, err := slow.Next(t.Context())
		if err != nil || event.Sequence != uint64(n) || event.Message != "original" {
			t.Fatalf("slow reader event %d = %+v, %v", n, event, err)
		}
	}
	run.Publish(Stderr, payload, true)
	event, err := slow.Next(t.Context())
	if err != nil || event.Sequence != QueueCapacity+11 || event.Source != Stderr || !event.Truncated {
		t.Fatalf("first event after loss = %+v, %v", event, err)
	}
}

func TestAdmissionAndExecutionIsolation(t *testing.T) {
	service := New("primary")
	first := service.Prepare()
	first.Activate()
	subs := make([]*Subscription, MaxSubscriptions)
	for i := range subs {
		var err error
		subs[i], err = service.Subscribe("primary")
		if err != nil {
			t.Fatal(err)
		}
		defer subs[i].Close()
	}
	first.Publish(Stdout, []byte("queued"), false)
	first.End()
	if _, err := subs[0].Next(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("closed execution returned a queued event: %v", err)
	}
	second := service.Prepare()
	second.Activate()
	if _, err := service.Subscribe("primary"); !errors.Is(err, ErrCapacity) {
		t.Fatalf("old transport handlers still own slots: %v", err)
	}
	subs[0].Close()
	subs[0].Close()
	fresh, err := service.Subscribe("primary")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	first.Publish(Stdout, []byte("late"), false)
	second.Publish(Stderr, []byte("new"), false)
	event, err := fresh.Next(t.Context())
	if err != nil || event.Execution != 2 || event.Sequence != 1 || event.Message != "new" {
		t.Fatalf("replacement first event = %+v, %v", event, err)
	}
}
