package jsonwire

import (
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type LeaseStatus struct {
	Revision    string        `json:"revision"`
	State       string        `json:"state"`
	Owner       string        `json:"owner"`
	ExecutionID string        `json:"execution_id"`
	Policy      string        `json:"policy"`
	ExpiresAt   *time.Time    `json:"expires_at"`
	StopEffect  domain.Effect `json:"stop_effect"`
	Error       string        `json:"expiry_error,omitempty"`
	ObservedAt  time.Time     `json:"observed_at"`
}
type LeaseReceipt struct {
	Status LeaseStatus `json:"status"`
	Token  string      `json:"token"`
}

func Lease(r domain.LeaseStatus) LeaseStatus {
	return LeaseStatus{Revision: strconv.FormatUint(r.Revision, 10), State: r.State, Owner: r.Owner, ExecutionID: r.ExecutionID, Policy: r.Policy, ExpiresAt: r.ExpiresAt, StopEffect: r.StopEffect, Error: r.Error, ObservedAt: r.ObservedAt}
}
func Receipt(r domain.LeaseReceipt) LeaseReceipt {
	return LeaseReceipt{Status: Lease(r.Status), Token: r.Token}
}
