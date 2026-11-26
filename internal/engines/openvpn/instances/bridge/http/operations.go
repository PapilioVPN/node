package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

func (h handlers) sessions(w http.ResponseWriter, r *http.Request) {
	report, err := h.service.Sessions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStatus(w, lifecycle.Status{}, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (h handlers) disconnect(w http.ResponseWriter, r *http.Request) {
	session, err := strconv.ParseUint(chi.URLParam(r, "session_id"), 10, 64)
	execution := r.URL.Query().Get("execution_id")
	if err != nil || execution == "" || r.ContentLength != 0 {
		writeInputError(w, err)
		return
	}
	if err := h.service.Disconnect(r.Context(), chi.URLParam(r, "id"), execution, session); err != nil {
		writeStatus(w, lifecycle.Status{}, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (h handlers) logs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	var after uint64
	if raw := query.Get("after_sequence"); raw != "" {
		var err error
		after, err = strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeInputError(w, err)
			return
		}
	}
	controller := http.NewResponseController(w)
	encoder := json.NewEncoder(w)
	started := false
	err := h.service.FollowLogs(r.Context(), chi.URLParam(r, "id"), query.Get("execution_id"), after, func(batch lifecycle.LogBatch) error {
		if err := controller.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
			return err
		}
		defer controller.SetWriteDeadline(time.Time{})
		if !started {
			w.Header().Set("Content-Type", "application/x-ndjson")
			w.Header().Set("Cache-Control", "no-store")
			started = true
		}
		if err := encoder.Encode(batch); err != nil {
			return err
		}
		return controller.Flush()
	})
	if err != nil && !started {
		writeStatus(w, lifecycle.Status{}, err)
	}
}
