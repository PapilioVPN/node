// Package http exposes Xray routing reads over Node's HTTP API.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
	"github.com/go-chi/chi/v5"
)

type balancerTargets struct {
	Tags []string `json:"tags"`
}

type balancerInfo struct {
	OverrideTarget  *string          `json:"override_target,omitempty"`
	ReportedTargets *balancerTargets `json:"reported_targets,omitempty"`
}

func Register(router chi.Router, service *state.Service) {
	const base = "/v1/xray/instances/{id}/routing"
	router.Get(base+"/rules", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		rules, err := service.ListRoutingRules(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeRules(w, rules)
	})
	router.Get(base+"/balancer", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["tag"]) != 1 {
			writeError(w, state.ErrInvalid)
			return
		}
		info, err := service.GetBalancerInfo(r.Context(), chi.URLParam(r, "id"), values.Get("tag"))
		if err != nil {
			writeError(w, err)
			return
		}
		result := balancerInfo{OverrideTarget: info.OverrideTarget}
		if info.ReportedTargets != nil {
			result.ReportedTargets = &balancerTargets{Tags: info.ReportedTargets}
		}
		writeJSON(w, result)
	})
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		http.Error(w, lifecycle.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, state.ErrBalancerNotFound):
		http.Error(w, state.ErrBalancerNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, state.ErrInvalid):
		http.Error(w, state.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, state.ErrBusy):
		http.Error(w, state.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, state.ErrTooLarge):
		http.Error(w, state.ErrTooLarge.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, context.Canceled):
		http.Error(w, "Xray routing request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "Xray routing request deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, state.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
