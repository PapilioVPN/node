package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

type statusResponse struct {
	ID                  string                `json:"id"`
	State               lifecycle.State       `json:"state"`
	NodeVersion         string                `json:"node_version"`
	OpenVPNVersion      string                `json:"openvpn_version,omitempty"`
	ExecutionID         string                `json:"execution_id,omitempty"`
	AppliedConfigSHA256 string                `json:"applied_config_sha256,omitempty"`
	StartAction         lifecycle.StartAction `json:"start_action,omitempty"`
}

type failureResponse struct {
	Message string          `json:"message"`
	Status  *statusResponse `json:"status,omitempty"`
}

type validationResponse struct {
	ID    string         `json:"id"`
	Valid bool           `json:"valid"`
	Issue *issueResponse `json:"issue,omitempty"`
}

type issueResponse struct {
	Code string `json:"code"`
	Line uint32 `json:"line,omitempty"`
}

func writeStatus(w http.ResponseWriter, result lifecycle.Status, err error) {
	status := statusResponse(result)
	if err == nil {
		writeJSON(w, http.StatusOK, status)
		return
	}
	code, message := publicError(err)
	failure := failureResponse{Message: message}
	if status.ID != "" {
		failure.Status = &status
	}
	writeJSON(w, code, failure)
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func publicError(err error) (int, string) {
	switch {
	case errors.Is(err, lifecycle.ErrRevocationDisabled):
		return http.StatusConflict, lifecycle.ErrRevocationDisabled.Error()
	case errors.Is(err, lifecycle.ErrRevocationRejected):
		return http.StatusBadRequest, lifecycle.ErrRevocationRejected.Error()
	case errors.Is(err, lifecycle.ErrRevocationUnavailable):
		return http.StatusServiceUnavailable, lifecycle.ErrRevocationUnavailable.Error()
	case errors.Is(err, lifecycle.ErrSessionNotFound):
		return http.StatusNotFound, lifecycle.ErrSessionNotFound.Error()
	case errors.Is(err, lifecycle.ErrNotReady):
		return http.StatusConflict, lifecycle.ErrNotReady.Error()
	case errors.Is(err, lifecycle.ErrExecutionChanged):
		return http.StatusConflict, lifecycle.ErrExecutionChanged.Error()
	case errors.Is(err, lifecycle.ErrManagement):
		return http.StatusServiceUnavailable, lifecycle.ErrManagement.Error()
	case errors.Is(err, lifecycle.ErrNotFound):
		return http.StatusNotFound, lifecycle.ErrNotFound.Error()
	case errors.Is(err, lifecycle.ErrBusy):
		return http.StatusConflict, lifecycle.ErrBusy.Error()
	case errors.Is(err, lifecycle.ErrTooLarge):
		return http.StatusRequestEntityTooLarge, lifecycle.ErrTooLarge.Error()
	case errors.Is(err, lifecycle.ErrRejected):
		return http.StatusBadRequest, lifecycle.ErrRejected.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "openvpn operation deadline exceeded"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "openvpn operation canceled"
	case errors.Is(err, lifecycle.ErrSourceUnavailable):
		return http.StatusServiceUnavailable, lifecycle.ErrSourceUnavailable.Error()
	case errors.Is(err, lifecycle.ErrCheckFailed):
		return http.StatusServiceUnavailable, lifecycle.ErrCheckFailed.Error()
	case errors.Is(err, lifecycle.ErrStopFailed):
		return http.StatusServiceUnavailable, lifecycle.ErrStopFailed.Error()
	default:
		return http.StatusServiceUnavailable, lifecycle.ErrStartFailed.Error()
	}
}
