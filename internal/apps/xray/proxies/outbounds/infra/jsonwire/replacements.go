package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

func fields(data []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if len(data) > 2<<20 {
		return nil, domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return nil, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, domain.ErrInvalidArgument
	}
	result := map[string]json.RawMessage{}
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
		if !ok || !found || result[name] != nil {
			return nil, domain.ErrInvalidArgument
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return nil, domain.ErrInvalidArgument
		}
		result[name] = value
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return nil, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, domain.ErrInvalidArgument
	}
	return result, nil
}
func textField(f map[string]json.RawMessage, key string, required bool) (string, error) {
	value, ok := f[key]
	if !ok && !required {
		return "", nil
	}
	var result string
	if !ok || json.Unmarshal(value, &result) != nil {
		return "", domain.ErrInvalidArgument
	}
	return result, nil
}

type PlanRequest struct {
	Control          Request
	DefaultPolicy    domain.DefaultPolicy
	ConnectionPolicy domain.ConnectionPolicy
}

func DecodePlan(data []byte) (PlanRequest, error) {
	f, err := fields(data, "instance_id", "tag", "configuration", "sources", "default_policy", "connection_policy")
	if err != nil {
		return PlanRequest{}, err
	}
	defaultPolicy, e1 := textField(f, "default_policy", true)
	connections, e2 := textField(f, "connection_policy", true)
	if err := errors.Join(e1, e2); err != nil {
		return PlanRequest{}, err
	}
	delete(f, "default_policy")
	delete(f, "connection_policy")
	controlData, err := json.Marshal(f)
	if err != nil {
		return PlanRequest{}, domain.ErrInvalidArgument
	}
	control, err := DecodeControl(controlData)
	if err != nil {
		return PlanRequest{}, err
	}
	if control.Tag == nil || control.HasSources == control.HasConfiguration {
		return PlanRequest{}, domain.ErrInvalidArgument
	}
	return PlanRequest{Control: control, DefaultPolicy: domain.DefaultPolicy(defaultPolicy), ConnectionPolicy: domain.ConnectionPolicy(connections)}, nil
}
func DecodePlanID(data []byte) (*string, string, error) {
	f, err := fields(data, "instance_id", "plan_id")
	if err != nil {
		return nil, "", err
	}
	id, e1 := textField(f, "instance_id", false)
	plan, e2 := textField(f, "plan_id", true)
	if err := errors.Join(e1, e2); err != nil {
		return nil, "", err
	}
	if len(plan) != 32 {
		return nil, "", domain.ErrInvalidArgument
	}
	if f["instance_id"] == nil {
		return nil, plan, nil
	}
	return &id, plan, nil
}
func DecodePage(data []byte) (string, domain.PageRequest, error) {
	if len(data) > 4096 {
		return "", domain.PageRequest{}, domain.ErrCapacity
	}
	f, err := fields(data, "instance_id", "page_size", "page_token")
	if err != nil {
		return "", domain.PageRequest{}, err
	}
	id, e1 := textField(f, "instance_id", true)
	token, e2 := textField(f, "page_token", false)
	var size uint32
	if raw := f["page_size"]; raw != nil {
		if json.Unmarshal(raw, &size) != nil {
			return "", domain.PageRequest{}, domain.ErrInvalidArgument
		}
	}
	return id, domain.PageRequest{PageSize: size, PageToken: token}, errors.Join(e1, e2)
}
func EncodePlan(p domain.ReplacementPlan) ([]byte, error) {
	return json.Marshal(struct {
		PlanID                      string    `json:"plan_id"`
		InstanceID                  string    `json:"instance_id"`
		Generation                  uint64    `json:"generation,string"`
		OwnerRevision               uint64    `json:"owner_revision,string"`
		Tag                         string    `json:"tag"`
		Mode                        string    `json:"mode"`
		Reason                      string    `json:"reason"`
		DefaultState                string    `json:"default_state"`
		CurrentSHA256               string    `json:"current_sha256"`
		CandidateSHA256             string    `json:"candidate_sha256"`
		DefaultMayChange            bool      `json:"default_may_change"`
		ExistingConnectionsRetained bool      `json:"existing_connections_retained"`
		NativeResourcesMayRemain    bool      `json:"native_resources_may_remain"`
		NextAction                  string    `json:"next_action"`
		CreatedAt                   time.Time `json:"created_at"`
		ExpiresAt                   time.Time `json:"expires_at"`
		Validation                  string    `json:"validation"`
		TrafficGapPossible          bool      `json:"traffic_gap_possible"`
	}{p.ID, p.InstanceID, p.Generation, p.OwnerRevision, p.Tag, p.Mode, p.Reason, p.DefaultState, p.CurrentSHA256, p.CandidateSHA256, p.DefaultMayChange, p.ExistingConnectionsRetained, p.NativeResourcesMayRemain, p.NextAction, p.CreatedAt, p.ExpiresAt, p.Validation, p.TrafficGapPossible})
}
func EncodeReplacement(r domain.ReplacementResult) ([]byte, error) {
	type step struct {
		Kind                    string     `json:"kind"`
		Effect                  string     `json:"effect"`
		Attempted               bool       `json:"attempted"`
		Acknowledged            bool       `json:"acknowledged"`
		RequiresInstanceRestart bool       `json:"requires_instance_restart"`
		Error                   string     `json:"error"`
		CompletedAt             *time.Time `json:"completed_at"`
	}
	steps := make([]step, 0, len(r.Steps))
	for _, s := range r.Steps {
		steps = append(steps, step{s.Kind, s.Effect, s.Attempted, s.Acknowledged, s.RequiresInstanceRestart, s.Error, s.CompletedAt})
	}
	return json.Marshal(struct {
		PlanID                   string     `json:"plan_id"`
		InstanceID               string     `json:"instance_id"`
		Generation               uint64     `json:"generation,string"`
		Tag                      string     `json:"tag"`
		Outcome                  string     `json:"outcome"`
		Error                    string     `json:"error"`
		RequiresInstanceRestart  bool       `json:"requires_instance_restart"`
		DefaultMayChange         bool       `json:"default_may_change"`
		NativeResourcesMayRemain bool       `json:"native_resources_may_remain"`
		Steps                    []step     `json:"steps"`
		StartedAt                time.Time  `json:"started_at"`
		CompletedAt              *time.Time `json:"completed_at"`
	}{r.PlanID, r.InstanceID, r.Generation, r.Tag, r.Outcome, r.Error, r.RequiresInstanceRestart, r.DefaultMayChange, r.NativeResourcesMayRemain, steps, r.StartedAt, r.CompletedAt})
}
