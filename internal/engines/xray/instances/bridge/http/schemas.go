package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
)

type statusResponse struct {
	ID             string                `json:"id"`
	State          lifecycle.State       `json:"state"`
	XrayVersion    string                `json:"xray_version,omitempty"`
	NodeVersion    string                `json:"node_version"`
	StartAction    lifecycle.StartAction `json:"start_action,omitempty"`
	LeaseExpiresAt *time.Time            `json:"keep_alive_expires_at,omitempty"`
}

type failureResponse struct {
	Message  string                       `json:"message"`
	Reason   lifecycle.StartFailureReason `json:"reason,omitempty"`
	ExitCode *int32                       `json:"exit_code,omitempty"`
	Status   *statusResponse              `json:"status,omitempty"`
}

func writeResult(w http.ResponseWriter, result lifecycle.Status, err error) {
	response := statusResponse{ID: result.ID, State: result.State, XrayVersion: result.XrayVersion,
		NodeVersion: result.NodeVersion, StartAction: result.StartAction}
	if !result.LeaseExpiresAt.IsZero() {
		response.LeaseExpiresAt = new(result.LeaseExpiresAt.UTC())
	}
	w.Header().Set("Cache-Control", "no-store")
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	failure, code := publicError(err)
	if result.ID == "" {
		http.Error(w, failure.Message, code)
		return
	}
	failure.Status = &response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(failure)
}

func publicError(err error) (failureResponse, int) {
	var failure *lifecycle.StartError
	switch {
	case errors.As(err, &failure):
		code := http.StatusServiceUnavailable
		if failure.Reason == lifecycle.ConfigRejected {
			code = http.StatusBadRequest
		}
		return failureResponse{Message: failure.Error(), Reason: failure.Reason, ExitCode: failure.ExitCode}, code
	case errors.Is(err, lifecycle.ErrNotFound):
		return failureResponse{Message: lifecycle.ErrNotFound.Error()}, http.StatusNotFound
	case errors.Is(err, lifecycle.ErrInvalidStart):
		return failureResponse{Message: lifecycle.ErrInvalidStart.Error()}, http.StatusBadRequest
	case errors.Is(err, lifecycle.ErrBusy):
		return failureResponse{Message: lifecycle.ErrBusy.Error()}, http.StatusConflict
	case errors.Is(err, lifecycle.ErrUnavailable):
		return failureResponse{Message: lifecycle.ErrUnavailable.Error()}, http.StatusServiceUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return failureResponse{Message: "xray operation deadline exceeded"}, http.StatusGatewayTimeout
	case errors.Is(err, context.Canceled):
		return failureResponse{Message: "xray operation canceled"}, http.StatusRequestTimeout
	case errors.Is(err, lifecycle.ErrStopFailed):
		return failureResponse{Message: lifecycle.ErrStopFailed.Error()}, http.StatusServiceUnavailable
	default:
		return failureResponse{Message: "xray instance unavailable"}, http.StatusServiceUnavailable
	}
}
