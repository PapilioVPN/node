// Package jsonwire defines bounded live-inbound envelopes for HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	sourcewire "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
)

type Request struct {
	HasPageSize        bool
	PageSize           uint32
	Cursor             *string
	Token              *string
	AllowInterruption  *bool
	DisconnectExisting *bool
	Sources            []configs.Source
	HasSources         bool
	InstanceID         *string
	Tag                *string
	Configuration      []byte
	HasConfiguration   bool
}

func Decode(data []byte) (request Request, err error) {
	if len(data) > 2<<20 {
		return request, domain.ErrCapacity
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return request, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return request, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return request, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "page_size":
			var value *uint32
			err = decoder.Decode(&value)
			if value == nil {
				return request, domain.ErrInvalidArgument
			}
			request.PageSize = *value
			request.HasPageSize = true
		case "cursor":
			err = decoder.Decode(&request.Cursor)
			if request.Cursor == nil {
				return request, domain.ErrInvalidArgument
			}
		case "token":
			err = decoder.Decode(&request.Token)
			if request.Token == nil {
				return request, domain.ErrInvalidArgument
			}
		case "allow_interruption":
			err = decoder.Decode(&request.AllowInterruption)
			if request.AllowInterruption == nil {
				return request, domain.ErrInvalidArgument
			}
		case "disconnect_existing":
			err = decoder.Decode(&request.DisconnectExisting)
			if request.DisconnectExisting == nil {
				return request, domain.ErrInvalidArgument
			}
		case "instance_id":
			err = decoder.Decode(&request.InstanceID)
			if request.InstanceID == nil {
				return request, domain.ErrInvalidArgument
			}
		case "tag":
			err = decoder.Decode(&request.Tag)
			if request.Tag == nil {
				return request, domain.ErrInvalidArgument
			}
		case "sources":
			request.HasSources = true
			request.Sources, err = sourcewire.DecodeSources(decoder)
			if errors.Is(err, configs.ErrTooLarge) {
				return request, domain.ErrCapacity
			}
		case "configuration":
			request.HasConfiguration = true
			err = decoder.Decode(&request.Configuration)
		default:
			return request, domain.ErrInvalidArgument
		}
		if err != nil {
			return request, domain.ErrInvalidArgument
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return request, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return request, domain.ErrInvalidArgument
	}
	return request, nil
}
func EncodeRecords(result domain.Records) ([]byte, error) {
	type record struct {
		Tag           string `json:"tag"`
		Configuration []byte `json:"configuration"`
	}
	records := make([]record, len(result.Inbounds))
	for i, item := range result.Inbounds {
		records[i] = record{item.Tag, item.Configuration}
	}
	return json.Marshal(struct {
		InstanceID string     `json:"instance_id"`
		Generation uint64     `json:"generation,string"`
		Inbounds   []record   `json:"inbounds"`
		ObservedAt time.Time  `json:"observed_at"`
		DurationNS int64      `json:"duration_ns,string"`
		NextCursor string     `json:"next_cursor"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}{result.InstanceID, result.Generation, records, result.ObservedAt, int64(result.Duration), result.NextCursor, optionalTime(result.ExpiresAt)})
}
func EncodeTags(result domain.Tags) ([]byte, error) {
	tags := result.Tags
	if tags == nil {
		tags = []string{}
	}
	return json.Marshal(struct {
		InstanceID string     `json:"instance_id"`
		Generation uint64     `json:"generation,string"`
		Tags       []string   `json:"tags"`
		ObservedAt time.Time  `json:"observed_at"`
		DurationNS int64      `json:"duration_ns,string"`
		NextCursor string     `json:"next_cursor"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}{result.InstanceID, result.Generation, tags, result.ObservedAt, int64(result.Duration), result.NextCursor, optionalTime(result.ExpiresAt)})
}

type operation struct {
	Replacement             *ReplacementProgress `json:"replacement,omitempty"`
	InstanceID              string               `json:"instance_id"`
	Generation              uint64               `json:"generation,string"`
	Kind                    string               `json:"kind"`
	Tag                     string               `json:"tag"`
	Effect                  domain.Effect        `json:"effect"`
	Acknowledged            bool                 `json:"acknowledged"`
	RequiresInstanceRestart bool                 `json:"requires_instance_restart"`
	ObservedAt              *time.Time           `json:"observed_at"`
	DurationNS              int64                `json:"duration_ns,string"`
}

func operationResult(result domain.Operation) operation {
	response := operation{Replacement: ProgressResult(result.Replacement), InstanceID: result.InstanceID, Generation: result.Generation, Kind: result.Kind, Tag: result.Tag, Effect: result.Effect, Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNS: int64(result.Duration)}
	if !result.ObservedAt.IsZero() {
		response.ObservedAt = &result.ObservedAt
	}
	return response
}
func EncodeOperation(result domain.Operation, code string) ([]byte, error) {
	return json.Marshal(struct {
		Operation operation `json:"operation"`
		Error     string    `json:"error,omitempty"`
	}{operationResult(result), code})
}
func EncodeStatus(result domain.ControlStatus) ([]byte, error) {
	var last *operation
	if result.LastOperation != nil {
		copy := operationResult(*result.LastOperation)
		last = &copy
	}
	return json.Marshal(struct {
		InstanceID              string     `json:"instance_id"`
		Generation              uint64     `json:"generation,string"`
		ExecutionState          string     `json:"execution_state"`
		APIConfigured           bool       `json:"api_configured"`
		RequestInFlight         bool       `json:"request_in_flight"`
		RequiresInstanceRestart bool       `json:"requires_instance_restart"`
		LastOperation           *operation `json:"last_operation"`
		ObservedAt              time.Time  `json:"observed_at"`
	}{result.InstanceID, result.Generation, result.ExecutionState, result.APIConfigured, result.RequestInFlight, result.RequiresInstanceRestart, last, result.ObservedAt})
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
func (r Request) Page() domain.PageRequest {
	p := domain.PageRequest{Size: r.PageSize}
	if r.Cursor != nil {
		p.Cursor = *r.Cursor
	}
	return p
}
func (r Request) HasPage() bool { return r.HasPageSize || r.Cursor != nil }
func (r Request) HasReplacement() bool {
	return r.Token != nil || r.AllowInterruption != nil || r.DisconnectExisting != nil
}
func EncodeSummaries(result domain.Summaries) ([]byte, error) {
	if result.Inbounds == nil {
		result.Inbounds = []domain.Summary{}
	}
	return json.Marshal(struct {
		InstanceID string    `json:"instance_id"`
		Generation uint64    `json:"generation,string"`
		Inbounds   []Summary `json:"inbounds"`
		ObservedAt time.Time `json:"observed_at"`
		DurationNS int64     `json:"duration_ns,string"`
		NextCursor string    `json:"next_cursor"`
		ExpiresAt  time.Time `json:"expires_at"`
	}{result.InstanceID, result.Generation, summariesResult(result.Inbounds), result.ObservedAt, int64(result.Duration), result.NextCursor, result.ExpiresAt})
}
func EncodeCompatibility(result domain.Compatibility) ([]byte, error) {
	return json.Marshal(struct {
		InstanceID string    `json:"instance_id"`
		Generation uint64    `json:"generation,string"`
		Inbound    Summary   `json:"inbound"`
		ObservedAt time.Time `json:"observed_at"`
		DurationNS int64     `json:"duration_ns,string"`
	}{result.InstanceID, result.Generation, summaryResult(result.Inbound), result.ObservedAt, int64(result.Duration)})
}
