package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"io"
	"strconv"
	"time"
	"unicode/utf8"
)

// DecodeIntent accepts only the selected operation's explicit string fields.
// Revisions use canonical decimal strings, preserving required zero presence.
func DecodeIntent(data []byte, allowed ...string) (map[string]string, error) {
	if len(data) > 4096 {
		return nil, domain.ErrTooLarge
	}
	if !utf8.Valid(data) {
		return nil, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, domain.ErrInvalidArgument
	}
	result := map[string]string{}
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return nil, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		found := false
		for _, a := range allowed {
			found = found || a == name
		}
		if _, duplicate := result[name]; !ok || !found || duplicate {
			return nil, domain.ErrInvalidArgument
		}
		var value *string
		if d.Decode(&value) != nil || value == nil {
			return nil, domain.ErrInvalidArgument
		}
		result[name] = *value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, domain.ErrInvalidArgument
	}
	return result, nil
}
func Revision(input map[string]string) (uint64, error) {
	text, present := input["expected_revision"]
	value, err := strconv.ParseUint(text, 10, 64)
	if !present || err != nil || strconv.FormatUint(value, 10) != text {
		return 0, domain.ErrInvalidArgument
	}
	return value, nil
}
func EncodePlan(p domain.Plan) ([]byte, error) {
	type difference struct {
		Section string `json:"section"`
		Before  uint32 `json:"before_count"`
		After   uint32 `json:"after_count"`
	}
	diff := make([]difference, len(p.Differences))
	for i, d := range p.Differences {
		diff[i] = difference{d.Section, d.Before, d.After}
	}
	return json.Marshal(struct {
		ID          string           `json:"plan_id"`
		InstanceID  string           `json:"instance_id"`
		Expected    uint64           `json:"expected_revision,string"`
		Previous    string           `json:"previous_sha256"`
		Candidate   string           `json:"candidate_sha256"`
		ExecutionID string           `json:"execution_id"`
		Generation  uint64           `json:"generation,string"`
		Mode        domain.ApplyMode `json:"mode"`
		Reason      string           `json:"reason"`
		Differences []difference     `json:"differences"`
		Retained    bool             `json:"connections_retained"`
		CreatedAt   time.Time        `json:"created_at"`
		ExpiresAt   time.Time        `json:"expires_at"`
	}{p.ID, p.InstanceID, p.ExpectedRevision, p.PreviousSHA256, p.CandidateSHA256, p.ExecutionID, p.Generation, p.Mode, p.Reason, diff, p.ConnectionsRetained, p.CreatedAt, p.ExpiresAt})
}
func persistence(p domain.Persistence) persistenceResponse {
	return persistenceResponse{metadataResult(p.Metadata), p.Published, p.PublicationUnknown, p.Durable}
}

type recoveryResponse struct {
	ID          string              `json:"recovery_id"`
	Expected    uint64              `json:"expected_revision,string"`
	Revision    uint64              `json:"revision,string"`
	Outcome     string              `json:"outcome"`
	Effect      string              `json:"engine_effect"`
	Readiness   string              `json:"readiness"`
	Error       string              `json:"error"`
	ExecutionID string              `json:"execution_id"`
	Generation  uint64              `json:"generation,string"`
	Persistence persistenceResponse `json:"persistence"`
	StartedAt   time.Time           `json:"started_at"`
	CompletedAt *time.Time          `json:"completed_at"`
}

func EncodeApplication(a domain.Application) ([]byte, error) {
	var recovery *recoveryResponse
	if r := a.Recovery; r != nil {
		recovery = &recoveryResponse{r.ID, r.ExpectedRevision, r.Revision, r.Outcome, r.EngineEffect, r.Readiness, r.Error, r.ExecutionID, r.Generation, persistence(r.Persistence), r.StartedAt, r.CompletedAt}
	}
	return json.Marshal(struct {
		ID                string              `json:"application_id"`
		InstanceID        string              `json:"instance_id"`
		Epoch             string              `json:"epoch"`
		Expected          uint64              `json:"expected_revision,string"`
		Revision          uint64              `json:"revision,string"`
		Previous          uint64              `json:"previous_revision,string"`
		Candidate         string              `json:"candidate_sha256"`
		Mode              domain.ApplyMode    `json:"mode"`
		Outcome           string              `json:"outcome"`
		Phase             string              `json:"phase"`
		Effect            string              `json:"engine_effect"`
		Acknowledged      bool                `json:"acknowledged"`
		Readiness         string              `json:"readiness"`
		Error             string              `json:"error"`
		ExecutionID       string              `json:"execution_id"`
		Generation        uint64              `json:"generation,string"`
		Persistence       persistenceResponse `json:"persistence"`
		ReceiptDurable    bool                `json:"receipt_durable"`
		RecoveryAvailable bool                `json:"recovery_available"`
		RequiresRecovery  bool                `json:"requires_recovery"`
		StartedAt         time.Time           `json:"started_at"`
		CompletedAt       *time.Time          `json:"completed_at"`
		Recovery          *recoveryResponse   `json:"recovery"`
	}{a.ID, a.InstanceID, a.Epoch, a.ExpectedRevision, a.Revision, a.PreviousRevision, a.CandidateSHA256, a.Mode, a.Outcome, a.Phase, a.EngineEffect, a.Acknowledged, a.Readiness, a.Error, a.ExecutionID, a.Generation, persistence(a.Persistence), a.ReceiptDurable, a.RecoveryAvailable, a.RequiresRecovery, a.StartedAt, a.CompletedAt, recovery})
}
