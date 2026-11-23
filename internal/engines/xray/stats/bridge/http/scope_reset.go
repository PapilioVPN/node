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
		_, _ = w.Write(appendScopeReport(outcome, report))
	}
}

func appendScopeReport(outcome string, report []traffic.CounterReset) []byte {
	size := 40 + len(report)*86
	for i := range report {
		size += len(report[i].Name)
	}
	body := append(make([]byte, 0, size), `{"outcome":"`...)
	body = append(body, outcome...)
	body = append(body, `","counters":[`...)
	for i := range report {
		row := &report[i]
		if i != 0 {
			body = append(body, ',')
		}
		body = append(body, `{"name":`...)
		body = appendCounterName(body, row.Name)
		body = append(body, `,"direction":"`...)
		body = append(body, string(row.Direction)...)
		body = append(body, `","result":{"effect":"`...)
		body = append(body, string(row.Effect)...)
		body = append(body, '"')
		if row.Effect == traffic.ResetAcknowledged {
			body = append(body, `,"previous_bytes":`...)
			body = strconv.AppendInt(body, row.PreviousBytes, 10)
		}
		body = append(body, '}', '}')
	}
	return append(body, "]}\n"...)
}

func appendCounterName(body []byte, name string) []byte {
	for i := range len(name) {
		b := name[i]
		if b < 0x20 || b >= 0x80 || b == '"' || b == '\\' || b == '<' || b == '>' || b == '&' {
			quoted, _ := json.Marshal(name)
			return append(body, quoted...)
		}
	}
	body = append(body, '"')
	body = append(body, name...)
	return append(body, '"')
}
