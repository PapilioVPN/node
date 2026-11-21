// Package jsonwire owns logger-control envelopes shared by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
)

func DecodeRequest(data []byte) (string, error) {
	if len(data) > 2048 {
		return "", domain.ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", domain.ErrInvalidArgument
	}
	if key, err := decoder.Token(); err != nil || key != "instance_id" {
		return "", domain.ErrInvalidArgument
	}
	var id string
	if err := decoder.Decode(&id); err != nil {
		return "", domain.ErrInvalidArgument
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", domain.ErrInvalidArgument
	}
	return id, nil
}

type operationResponse struct {
	InstanceID              string        `json:"instance_id"`
	Generation              uint64        `json:"generation,string"`
	Effect                  domain.Effect `json:"effect"`
	Acknowledged            bool          `json:"acknowledged"`
	RequiresInstanceRestart bool          `json:"requires_instance_restart"`
	ObservedAt              *time.Time    `json:"observed_at"`
	DurationNS              int64         `json:"duration_ns,string"`
}

func operationResult(result domain.Operation) operationResponse {
	response := operationResponse{InstanceID: result.InstanceID, Generation: result.Generation, Effect: result.Effect, Acknowledged: result.Acknowledged, RequiresInstanceRestart: result.RequiresInstanceRestart, DurationNS: int64(result.Duration)}
	if !result.ObservedAt.IsZero() {
		response.ObservedAt = &result.ObservedAt
	}
	return response
}
func EncodeOperation(result domain.Operation, code string) ([]byte, error) {
	return json.Marshal(struct {
		Operation operationResponse `json:"operation"`
		Error     string            `json:"error,omitempty"`
	}{operationResult(result), code})
}
func EncodeStatus(result domain.ControlStatus) ([]byte, error) {
	var last *operationResponse
	if result.LastOperation != nil {
		value := operationResult(*result.LastOperation)
		last = &value
	}
	return json.Marshal(struct {
		InstanceID              string             `json:"instance_id"`
		Generation              uint64             `json:"generation,string"`
		ExecutionState          string             `json:"execution_state"`
		APIConfigured           bool               `json:"api_configured"`
		RequestInFlight         bool               `json:"request_in_flight"`
		RequiresInstanceRestart bool               `json:"requires_instance_restart"`
		LastOperation           *operationResponse `json:"last_operation"`
		ObservedAt              time.Time          `json:"observed_at"`
	}{result.InstanceID, result.Generation, string(result.ExecutionState), result.APIConfigured, result.RequestInFlight, result.RequiresInstanceRestart, last, result.ObservedAt})
}
