package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/go-chi/chi/v5"
)

func resetTrafficScope(service *traffic.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values) != 1 || len(values["max_counters"]) != 1 || (r.Body != nil && r.Body != http.NoBody) {
			writeResetError(w, traffic.ErrInvalid)
			return
		}
		limit, err := strconv.ParseUint(values.Get("max_counters"), 10, 32)
		if err != nil {
			writeResetError(w, traffic.ErrInvalid)
			return
		}
		report, err := service.ResetScope(r.Context(), traffic.ScopeReset{
			ID: chi.URLParam(r, "id"), Scope: traffic.Scope(chi.URLParam(r, "scope")), MaxCounters: uint32(limit),
		})
		if err != nil && report == nil {
			writeResetError(w, err)
			return
		}
		outcome := "complete"
		switch {
		case errors.Is(err, traffic.ErrResetUnknown):
			outcome = "unknown"
			w.WriteHeader(http.StatusBadGateway)
		case err != nil:
			outcome = "interrupted"
			w.WriteHeader(http.StatusConflict)
		}
		type counter struct {
			Name      string            `json:"name"`
			Direction traffic.Direction `json:"direction"`
			Result    directionReset    `json:"result"`
		}
		counters := make([]counter, len(report))
		for i := range report {
			row := &report[i]
			counters[i] = counter{Name: row.Name, Direction: row.Direction, Result: directionResponse(&row.DirectionReset)}
		}
		_ = json.NewEncoder(w).Encode(struct {
			Outcome  string    `json:"outcome"`
			Counters []counter `json:"counters"`
		}{outcome, counters})
	}
}
