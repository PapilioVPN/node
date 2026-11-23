package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/online"
	"github.com/go-chi/chi/v5"
)

type onlineCount struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type onlineIPs struct {
	Name                string           `json:"name"`
	LastSeenUnixSeconds map[string]int64 `json:"last_seen_unix_seconds"`
}

func RegisterOnline(router chi.Router, service *online.Service) {
	const base = "/v1/xray/instances/{id}/stats/online"
	router.Get(base+"/count", readOnline(func(ctx context.Context, id, name string) (onlineCount, error) {
		count, err := service.Count(ctx, id, name)
		return onlineCount{Name: name, Count: count}, err
	}))
	router.Get(base+"/ips", readOnline(func(ctx context.Context, id, name string) (onlineIPs, error) {
		ips, err := service.IPs(ctx, id, name)
		if ips == nil && err == nil {
			ips = map[string]int64{}
		}
		return onlineIPs{Name: name, LastSeenUnixSeconds: ips}, err
	}))
	router.Get(base+"/users", listOnlineUsers(service))
}

func listOnlineUsers(service *online.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		names, err := service.Users(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			writeOnlineError(w, err)
			return
		}
		if names == nil {
			names = []string{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Names []string `json:"names"`
		}{Names: names})
	}
}

func readOnline[T any](query func(context.Context, string, string) (T, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil || len(values["name"]) != 1 {
			writeOnlineError(w, online.ErrInvalid)
			return
		}
		result, err := query(r.Context(), chi.URLParam(r, "id"), values.Get("name"))
		if err != nil {
			writeOnlineError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	}
}

func writeOnlineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound), errors.Is(err, online.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, online.ErrInvalid):
		http.Error(w, online.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, online.ErrBusy):
		http.Error(w, online.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, online.ErrTooLarge):
		http.Error(w, online.ErrTooLarge.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, context.Canceled):
		http.Error(w, "Xray online statistics request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "Xray online statistics request deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, online.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
