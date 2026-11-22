// Package jsonwire owns the allowlisted shareable diagnostic wire model.
package jsonwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

const MaxRequestBytes = 1024

func Decode(data []byte) (domain.Request, error) {
	var r domain.Request
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return r, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return r, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		if err != nil || !ok || seen[key] {
			return r, domain.ErrInvalidArgument
		}
		seen[key] = true
		switch key {
		case "instance_id":
			var v *string
			if err := d.Decode(&v); err != nil || v == nil {
				return r, domain.ErrInvalidArgument
			}
			r.InstanceID = *v
		case "max_age_seconds":
			var v *uint32
			if err := d.Decode(&v); err != nil || v == nil {
				return r, domain.ErrInvalidArgument
			}
			r.MaxAgeSeconds = *v
		default:
			return r, domain.ErrInvalidArgument
		}
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') {
		return r, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || !seen["instance_id"] {
		return r, domain.ErrInvalidArgument
	}
	return r, nil
}

type Evidence struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason"`
	Source     string     `json:"source"`
	ObservedAt *time.Time `json:"observed_at"`
	Freshness  string     `json:"freshness"`
}
type Binary struct {
	Evidence
	Version      string `json:"version"`
	Build        string `json:"build"`
	GoVersion    string `json:"go_version"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Scope        string `json:"scope"`
}
type Execution struct {
	Evidence
	LifecycleState  string     `json:"lifecycle_state"`
	Generation      uint64     `json:"generation,string"`
	AppliedRevision *uint64    `json:"applied_revision,string"`
	StartedAt       *time.Time `json:"started_at"`
	ExitedAt        *time.Time `json:"exited_at"`
	ExitCode        *int32     `json:"exit_code"`
	FailureReason   string     `json:"failure_reason"`
	CleanupReason   string     `json:"cleanup_reason"`
}
type Configuration struct {
	Evidence
	DesiredRevision *uint64    `json:"desired_revision,string"`
	UpdatedAt       *time.Time `json:"updated_at"`
	Durable         *bool      `json:"durable"`
	MatchesApplied  *bool      `json:"matches_applied"`
}
type Capability struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Basis string `json:"basis"`
}
type Native struct {
	Evidence
	Generation    *uint64      `json:"generation,string"`
	Runtime       Evidence     `json:"runtime"`
	Discovery     Evidence     `json:"discovery"`
	UptimeSeconds *uint64      `json:"uptime_seconds,string"`
	Capabilities  []Capability `json:"capabilities"`
}
type Report struct {
	InstanceID    string        `json:"instance_id"`
	ObservedAt    time.Time     `json:"observed_at"`
	DurationNS    int64         `json:"duration_ns,string"`
	MaxAgeSeconds uint32        `json:"max_age_seconds"`
	Consistency   string        `json:"consistency"`
	Execution     Execution     `json:"execution"`
	Configuration Configuration `json:"configuration"`
	Binary        Binary        `json:"binary"`
	Native        Native        `json:"native_api"`
}

func evidence(e domain.Evidence) Evidence {
	return Evidence{e.State, e.Reason, e.Source, e.ObservedAt, e.Freshness}
}
func Present(r domain.Report) Report {
	e, c, b, n := r.Execution, r.Configuration, r.Binary, r.Native
	result := Report{InstanceID: r.InstanceID, ObservedAt: r.ObservedAt, DurationNS: int64(r.Duration), MaxAgeSeconds: r.MaxAgeSeconds, Consistency: r.Consistency,
		Execution:     Execution{evidence(e.Evidence), e.State, e.Generation, e.AppliedRevision, e.StartedAt, e.ExitedAt, e.ExitCode, e.FailureReason, e.CleanupReason},
		Configuration: Configuration{evidence(c.Evidence), c.DesiredRevision, c.UpdatedAt, c.Durable, c.MatchesApplied},
		Binary:        Binary{evidence(b.Evidence), b.Version, b.Build, b.GoVersion, b.OS, b.Architecture, b.Scope},
		Native:        Native{evidence(n.Evidence), n.Generation, evidence(n.Runtime), evidence(n.Discovery), n.UptimeSeconds, make([]Capability, 0, len(n.Capabilities))},
	}
	for _, c := range n.Capabilities {
		result.Native.Capabilities = append(result.Native.Capabilities, Capability{c.Name, c.State, c.Basis})
	}
	return result
}
func Encode(r domain.Report) ([]byte, error) {
	data, err := json.Marshal(Present(r))
	if err != nil {
		return nil, domain.ErrUnavailable
	}
	if len(data) > domain.MaxReportBytes {
		return nil, domain.ErrCapacity
	}
	return data, nil
}
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
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	default:
		return "unavailable"
	}
}
func Failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{ErrorCode(err)})
	return data
}
