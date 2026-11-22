package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

func TestInventoryStableBoundedWithoutNativeRead(t *testing.T) {
	bindings := make([]Binding, 0, 51)
	for i := 50; i >= 0; i-- {
		bindings = append(bindings, Binding{ID: fmt.Sprintf("instance-%02d", i), Engine: "xray", Runtime: &runtimeFixture{}})
	}
	s, err := New(bindings)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.List(context.Background(), "", 50)
	if err != nil || len(first.Items) != 50 || first.Items[0].ID != "instance-00" || first.NextAfter != "instance-49" {
		t.Fatal(first, err)
	}
	last, err := s.List(context.Background(), first.NextAfter, 50)
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != "instance-50" || last.NextAfter != "" {
		t.Fatal(last, err)
	}
	if _, err := s.List(context.Background(), "", 51); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
}
func TestHealthyRepairPreservesExecutionAndFailedStartAttemptsBounded(t *testing.T) {
	runtime := &runtimeFixture{execution: domain.Execution{State: domain.Running, Generation: 7, ExecutionID: "current"}}
	s, _ := New([]Binding{{ID: "one", Engine: "xray", Runtime: runtime}})
	result, err := s.Repair(context.Background(), "one", domain.RepairRequest{MaxAttempts: 3})
	if err != nil || !result.Ready || result.Attempts != 0 || runtime.Observe().Generation != 7 {
		t.Fatal(result, err)
	}
	runtime.execution.State = domain.Exited
	runtime.failStart = true
	result, err = s.Repair(context.Background(), "one", domain.RepairRequest{MaxAttempts: 3})
	if err == nil || result.Attempts != 3 || result.Control.Effect != domain.NotApplied {
		t.Fatal(result, err)
	}
	if _, err := s.Repair(context.Background(), "one", domain.RepairRequest{MaxAttempts: 4}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
}
