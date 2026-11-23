// Package http exposes Xray routing reads over Node's HTTP API.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
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
	router.Post(base+"/tests", testRoute(service))
	router.Post(base+"/rules", addRule(service))
	router.Delete(base+"/rules", removeRule(service))
	router.Put(base+"/balancer/override", overrideBalancer(service))
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

type routeProbe struct {
	InboundTag   string            `json:"inbound_tag"`
	Network      string            `json:"network"`
	TargetIP     netip.Addr        `json:"target_ip"`
	TargetPort   uint32            `json:"target_port"`
	TargetDomain string            `json:"target_domain"`
	Protocol     string            `json:"protocol"`
	User         string            `json:"user"`
	Attributes   map[string]string `json:"attributes"`
}

type routeSelection struct {
	OutboundTag       string   `json:"outbound_tag"`
	OutboundGroupTags []string `json:"outbound_group_tags,omitempty"`
}

func testRoute(service *state.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var probe *routeProbe
		if !decodeJSON(w, r, &probe) || probe == nil {
			writeError(w, state.ErrInvalid)
			return
		}
		selection, err := service.TestRoute(r.Context(), chi.URLParam(r, "id"), state.RouteProbe(*probe))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, routeSelection(selection))
	}
}

func overrideBalancer(service *state.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values, err := url.ParseQuery(r.URL.RawQuery)
		var body struct {
			Target *string `json:"target"`
		}
		if err != nil || len(values["tag"]) != 1 || !decodeJSON(w, r, &body) {
			writeError(w, state.ErrInvalid)
			return
		}
		if err := service.OverrideBalancerTarget(r.Context(), chi.URLParam(r, "id"), values.Get("tag"), body.Target); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false
	}
	return decoder.Decode(new(json.RawMessage)) == io.EOF
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
	case errors.Is(err, state.ErrRuleExists):
		http.Error(w, state.ErrRuleExists.Error(), http.StatusConflict)
	case errors.Is(err, state.ErrNoRoute):
		http.Error(w, state.ErrNoRoute.Error(), http.StatusNotFound)
	case errors.Is(err, state.ErrMutationUnknown):
		http.Error(w, state.ErrMutationUnknown.Error(), http.StatusBadGateway)
	case errors.Is(err, state.ErrMutationFenced):
		http.Error(w, state.ErrMutationFenced.Error(), http.StatusConflict)
	case errors.Is(err, state.ErrMutationBusy):
		http.Error(w, state.ErrMutationBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, state.ErrUnsupported):
		http.Error(w, state.ErrUnsupported.Error(), http.StatusNotImplemented)
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
