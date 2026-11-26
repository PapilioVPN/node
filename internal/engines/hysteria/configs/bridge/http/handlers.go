// Package http exposes candidate config preflight on Node's HTTP API.
package http

import (
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/validation"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *validation.Service) {
	router.Post(httpapi.InstancePath+"/config-validations", func(w http.ResponseWriter, r *http.Request) {
		source, err := httpapi.ReadBody(w, r, preflight.MaxBytes)
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		result, err := service.ValidateConfig(r.Context(), validation.Request{ID: httpapi.ID(r), Source: source})
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		httpapi.WriteJSON(w, http.StatusOK, struct {
			ID    string          `json:"id"`
			Valid bool            `json:"valid"`
			Scope string          `json:"scope"`
			Issue preflight.Issue `json:"issue,omitempty"`
		}{result.ID, result.Issue == "", preflight.ValidationScope, result.Issue})
	})
}
