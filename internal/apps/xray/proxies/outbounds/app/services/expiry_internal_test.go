package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type expiryStatus struct{}

func (expiryStatus) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}

type expiryClient struct{}

func (expiryClient) Records(context.Context) ([]domain.Record, error) {
	panic("continuation must not refetch native records")
}
func (expiryClient) Add(context.Context, []byte) (domain.NativeResult, error) {
	panic("expired or consumed plan must not add")
}
func (expiryClient) Remove(context.Context, string) (domain.NativeResult, error) {
	panic("expired or consumed plan must not remove")
}

type expiryInspector struct{}

func (expiryInspector) Inspect(context.Context, []byte) (domain.Candidate, error) {
	panic("expired plan must not inspect")
}
func TestExpiredCandidateIsNotAppliedAndConsumedEvidenceSurvivesExpiry(t *testing.T) {
	service, _ := New([]Binding{{ID: "a", Client: expiryClient{}}}, expiryStatus{})
	r, _ := NewReplacements(service, expiryInspector{}, nil)
	id := token()
	saved := &replacementPlan{plan: domain.ReplacementPlan{ID: id, InstanceID: "a"}, expires: time.Now().Add(-time.Second), content: []byte("private candidate")}
	r.plans["a"] = saved
	if _, err := r.Apply(context.Background(), "a", id); !errors.Is(err, domain.ErrPlanExpired) || saved.result != nil || saved.content != nil {
		t.Fatal("expired plan retained or executed", err)
	}
	saved.result = &domain.ReplacementResult{PlanID: id, InstanceID: "a", Outcome: "completed", Steps: []domain.ReplacementStep{{Kind: "add", Acknowledged: true, Effect: "completed"}}}
	result, err := r.Apply(context.Background(), "a", id)
	if err != nil || result.Outcome != "completed" || !result.Steps[0].Acknowledged {
		t.Fatal(result, err)
	}
}
func TestExpiredContinuationHasNoNativeReadOrSuccessfulPage(t *testing.T) {
	service, _ := New([]Binding{{ID: "a", Client: expiryClient{}}}, expiryStatus{})
	id := token()
	service.pages[id] = recordPage{records: domain.Records{InstanceID: "a", Generation: 1, Outbounds: make([]domain.Record, 2)}, expires: time.Now().Add(-time.Second)}
	result, err := service.List(context.Background(), "a", domain.PageRequest{PageToken: id + ":1"})
	if !errors.Is(err, domain.ErrContinuation) || result.Outbounds != nil {
		t.Fatal(result, err)
	}
}
