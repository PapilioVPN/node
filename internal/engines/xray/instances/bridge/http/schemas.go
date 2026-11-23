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

func writeResult(w http.ResponseWriter, result lifecycle.Status, err error) {
	if err != nil {
		switch {
		case errors.Is(err, lifecycle.ErrNotFound):
			http.Error(w, lifecycle.ErrNotFound.Error(), http.StatusNotFound)
		case errors.Is(err, lifecycle.ErrBusy):
			http.Error(w, lifecycle.ErrBusy.Error(), http.StatusConflict)
		case errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, lifecycle.ErrStartFailed):
			http.Error(w, "xray operation deadline exceeded", http.StatusGatewayTimeout)
		case errors.Is(err, context.Canceled) && !errors.Is(err, lifecycle.ErrStartFailed):
			http.Error(w, "xray operation canceled", http.StatusRequestTimeout)
		case errors.Is(err, lifecycle.ErrStartFailed):
			http.Error(w, lifecycle.ErrStartFailed.Error(), http.StatusServiceUnavailable)
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
