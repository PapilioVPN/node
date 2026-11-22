// Package jsonwire defines the shared HTTP/NATS outbound health JSON contract.
package jsonwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
)

const MaxRequestBytes = 32 << 10

type Request struct {
	InstanceID string
	Tag        *string
	Page       domain.PageRequest
}

// Decode accepts a single exact or collection request with explicit tag presence.
// Missing tag is different from a present empty tag selecting anonymous evidence.
func Decode(data []byte, collection bool) (Request, error) {
	var r Request
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return r, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return r, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return r, domain.ErrInvalidArgument
		}
		seen[key] = true
		switch key {
		case "instance_id", "outbound_tag", "page_token":
			if (collection && key == "outbound_tag") || (!collection && key == "page_token") {
				return r, domain.ErrInvalidArgument
			}
			var value *string
			if err := d.Decode(&value); err != nil || value == nil {
				return r, domain.ErrInvalidArgument
			}
			switch key {
			case "instance_id":
				r.InstanceID = *value
			case "outbound_tag":
				r.Tag = value
			case "page_token":
				r.Page.PageToken = *value
			}
		case "page_size", "max_sample_age_seconds":
			if !collection && key == "page_size" {
				return r, domain.ErrInvalidArgument
			}
			var value *uint32
			if err := d.Decode(&value); err != nil || value == nil {
				return r, domain.ErrInvalidArgument
			}
			if key == "page_size" {
				r.Page.PageSize = *value
			} else {
				r.Page.MaxSampleAgeSeconds = *value
			}
		default:
			return r, domain.ErrInvalidArgument
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return r, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || !seen["instance_id"] || (!collection && r.Tag == nil) {
		return r, domain.ErrInvalidArgument
	}
	return r, nil
}

type HealthPing struct {
	All         int64 `json:"all,string"`
	Fail        int64 `json:"fail,string"`
	DeviationNS int64 `json:"deviation_ns,string"`
	AverageNS   int64 `json:"average_ns,string"`
	MaxNS       int64 `json:"max_ns,string"`
	MinNS       int64 `json:"min_ns,string"`
}
type Status struct {
	Tag              string      `json:"outbound_tag"`
	Alive            bool        `json:"alive"`
	DelayMS          int64       `json:"delay_ms,string"`
	LatencyMS        *int64      `json:"latency_ms,string"`
	LastSeenUnixSec  int64       `json:"last_seen_unix_sec,string"`
	LastTryUnixSec   int64       `json:"last_try_unix_sec,string"`
	Mode             string      `json:"mode"`
	HealthPing       *HealthPing `json:"health_ping"`
	SampledAt        *time.Time  `json:"sampled_at"`
	SampleAgeSeconds *int64      `json:"sample_age_seconds,string"`
	Freshness        string      `json:"freshness"`
}

func Present(s domain.Status) Status {
	v := Status{Tag: s.Tag, Alive: s.Alive, DelayMS: s.DelayMS, LatencyMS: s.LatencyMS, LastSeenUnixSec: s.LastSeenUnixSec, LastTryUnixSec: s.LastTryUnixSec, Mode: s.Mode, SampledAt: s.SampledAt, SampleAgeSeconds: s.SampleAgeSeconds, Freshness: s.Freshness}
	if h := s.HealthPing; h != nil {
		v.HealthPing = &HealthPing{h.All, h.Fail, h.DeviationNS, h.AverageNS, h.MaxNS, h.MinNS}
	}
	return v
}

type Metadata struct {
	InstanceID          string    `json:"instance_id"`
	Generation          uint64    `json:"generation,string"`
	Source              string    `json:"source"`
	ObservedAt          time.Time `json:"observed_at"`
	DurationNS          int64     `json:"duration_ns,string"`
	MaxSampleAgeSeconds uint32    `json:"max_sample_age_seconds"`
}

func EncodeObservation(o domain.Observation) ([]byte, error) {
	var value *Status
	if o.Status != nil {
		v := Present(*o.Status)
		value = &v
	}
	return json.Marshal(struct {
		Metadata
		Status *Status `json:"status"`
	}{Metadata{o.InstanceID, o.Generation, o.Source, o.ObservedAt, int64(o.Duration), o.MaxSampleAgeSeconds}, value})
}
func EncodePage(p domain.Page) ([]byte, error) {
	values := make([]Status, 0, len(p.Statuses))
	for _, v := range p.Statuses {
		values = append(values, Present(v))
	}
	return json.Marshal(struct {
		Metadata
		Statuses      []Status   `json:"statuses"`
		ServedAt      time.Time  `json:"served_at"`
		ExpiresAt     *time.Time `json:"expires_at"`
		Total         uint32     `json:"total"`
		NextPageToken string     `json:"next_page_token"`
	}{Metadata{p.InstanceID, p.Generation, p.Source, p.ObservedAt, int64(p.Duration), p.MaxSampleAgeSeconds}, values, p.ServedAt, p.ExpiresAt, p.Total, p.NextPageToken})
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
	case errors.Is(err, domain.ErrDisabled):
		return "api_not_configured"
	case errors.Is(err, domain.ErrUnsupported):
		return "unsupported"
	case errors.Is(err, domain.ErrNotRunning):
		return "not_running"
	case errors.Is(err, domain.ErrClosed):
		return "closed"
	case errors.Is(err, domain.ErrExecutionChanged):
		return "execution_changed"
	case errors.Is(err, domain.ErrContinuation):
		return "continuation_expired"
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
