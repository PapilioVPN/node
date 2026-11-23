package http

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"github.com/go-chi/chi/v5"
)

func addRule(service *state.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var body struct {
			Rule           json.RawMessage `json:"rule"`
			Mode           state.RuleMode  `json:"mode"`
			ConfirmReplace bool            `json:"confirm_replace"`
		}
		if !decodeJSON(w, r, &body) {
			writeError(w, state.ErrInvalid)
			return
		}
		change := state.RuleChange{Source: body.Rule, Mode: body.Mode, ConfirmReplace: body.ConfirmReplace}
		if err := service.AddRoutingRule(r.Context(), chi.URLParam(r, "id"), change); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func removeRule(service *state.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["tag"]) != 1 {
			writeError(w, state.ErrInvalid)
			return
		}
		if err := service.RemoveRoutingRule(r.Context(), chi.URLParam(r, "id"), values.Get("tag")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
