package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type ControlRequest struct {
	InstanceID       *string
	InboundTag       *string
	Email            *string
	Configuration    []byte
	HasConfiguration bool
}

func DecodeControl(data []byte) (request ControlRequest, err error) {
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
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return request, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			err = decoder.Decode(&request.InstanceID)
			if request.InstanceID == nil {
				return request, domain.ErrInvalidArgument
			}
		case "inbound_tag":
			err = decoder.Decode(&request.InboundTag)
			if request.InboundTag == nil {
				return request, domain.ErrInvalidArgument
			}
		case "email":
			err = decoder.Decode(&request.Email)
			if request.Email == nil {
				return request, domain.ErrInvalidArgument
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

type operation struct {
	InstanceID              string        `json:"instance_id"`
	InboundTag              string        `json:"inbound_tag"`
	Generation              uint64        `json:"generation,string"`
	Kind                    string        `json:"kind"`
	Email                   string        `json:"email"`
	Effect                  domain.Effect `json:"effect"`
	Acknowledged            bool          `json:"acknowledged"`
	RequiresInstanceRestart bool          `json:"requires_instance_restart"`
	ObservedAt              *time.Time    `json:"observed_at"`
	DurationNS              int64         `json:"duration_ns,string"`
}

func operationResult(result domain.Operation) operation {
	response := operation{InstanceID: result.InstanceID, InboundTag: result.InboundTag, Generation: result.Generation, Kind: result.Kind, Email: result.Email, Effect: result.Effect, Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNS: int64(result.Duration)}
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
