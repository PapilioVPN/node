// Package jsonwire owns the bounded online JSON contracts shared by HTTP/NATS.
package jsonwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

// DecodeSelection rejects missing, duplicate, null and unknown fields.
func DecodeSelection(data []byte) (s domain.Selection, err error) {
	if len(data) > 32<<10 {
		return s, domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return s, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return s, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return s, domain.ErrInvalidArgument
		}
		seen[name] = true
		var value *string
		if name != "instance_id" && name != "email" {
			return s, domain.ErrInvalidArgument
		}
		if err := d.Decode(&value); err != nil || value == nil {
			return s, domain.ErrInvalidArgument
		}
		if name == "instance_id" {
			s.InstanceID = *value
		} else {
			s.Email = *value
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return s, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || !seen["instance_id"] || !seen["email"] {
		return s, domain.ErrInvalidArgument
	}
	return s, nil
}

type observation struct {
	InstanceID string    `json:"instance_id"`
	Email      string    `json:"email"`
	Generation uint64    `json:"generation,string"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
}

func metadata(o domain.Observation) observation {
	return observation{o.InstanceID, o.Email, o.Generation, domain.Source, o.ObservedAt, int64(o.Duration)}
}
func EncodeStatus(s domain.Status) ([]byte, error) {
	return json.Marshal(struct {
		Observation observation  `json:"observation"`
		State       domain.State `json:"state"`
		Count       *int64       `json:"count,string"`
	}{metadata(s.Observation), s.State(), s.Count})
}
func EncodeIPs(s domain.IPList) ([]byte, error) {
	type ip struct {
		Address  string `json:"ip"`
		LastSeen int64  `json:"last_seen_unix_seconds,string"`
	}
	var rows []ip
	if s.Tracked {
		rows = make([]ip, len(s.IPs))
		for i, row := range s.IPs {
			rows[i] = ip{row.Address, row.LastSeenUnixSeconds}
		}
	}
	return json.Marshal(struct {
		Observation observation  `json:"observation"`
		State       domain.State `json:"state"`
		IPs         []ip         `json:"ips"`
	}{metadata(s.Observation), s.State(), rows})
}

// ErrorCode contains only public classifications, never native diagnostics.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrNotConfigured):
		return "facility_not_configured"
	case errors.Is(err, domain.ErrUnsupported):
		return "facility_unsupported"
	case errors.Is(err, domain.ErrObservationExpired):
		return "observation_expired"
	case errors.Is(err, domain.ErrIdentityUnsupported):
		return "native_identity_unsupported"
	case errors.Is(err, domain.ErrExecutionChanged):
		return "execution_changed"
	case errors.Is(err, domain.ErrNotRunning):
		return "engine_unavailable"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	default:
		return "unavailable"
	}
}
