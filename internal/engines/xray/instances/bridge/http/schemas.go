package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
)

type statusResponse struct {
	ID    string          `json:"id"`
	State lifecycle.State `json:"state"`
}

type startFailureResponse struct {
	Message  string                       `json:"message"`
	Reason   lifecycle.StartFailureReason `json:"reason"`
	ExitCode *int32                       `json:"exit_code,omitempty"`
}

func writeResult(w http.ResponseWriter, result lifecycle.Status, err error) {
	if err != nil {
		var failure *lifecycle.StartError
		switch {
		case errors.As(err, &failure):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(startFailureResponse{
				Message: failure.Error(), Reason: failure.Reason, ExitCode: failure.ExitCode,
			})
		case errors.Is(err, lifecycle.ErrNotFound):
			http.Error(w, lifecycle.ErrNotFound.Error(), http.StatusNotFound)
		case errors.Is(err, lifecycle.ErrBusy):
			http.Error(w, lifecycle.ErrBusy.Error(), http.StatusConflict)
		case errors.Is(err, context.DeadlineExceeded):
			http.Error(w, "xray operation deadline exceeded", http.StatusGatewayTimeout)
		case errors.Is(err, context.Canceled):
			http.Error(w, "xray operation canceled", http.StatusRequestTimeout)
		case errors.Is(err, lifecycle.ErrStopFailed):
			http.Error(w, lifecycle.ErrStopFailed.Error(), http.StatusServiceUnavailable)
		default:
			http.Error(w, "xray instance unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statusResponse{ID: result.ID, State: result.State})
}
