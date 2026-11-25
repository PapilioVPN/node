// Package http exposes WireGuard interface operations over HTTP.
package http

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/PapilioVPN/node/internal/engines/wireguard/stats/traffic"
	"github.com/go-chi/chi/v5"
)

type response struct {
	Name          string `json:"name"`
	MTU           uint32 `json:"mtu"`
	Up            bool   `json:"up"`
	AppliedSHA256 string `json:"applied_config_sha256,omitempty"`
}

func Register(router chi.Router, service *status.Service, peerStats *traffic.Service, managed *lifecycle.Service, validator *lifecycle.Validator, membership *peers.Membership) {
	const path = "/v1/wireguard/interfaces/{name}"
	router.Get(path+"/peers/stats", listPeerStats(peerStats))
	router.Get(path+"/peers", listPeers(membership))
	router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		result, err := service.GetStatus(r.Context(), interfaceName(r))
		if err != nil {
			writeError(w, err)
			return
		}
		writeStatus(w, result)
	})
	router.Post(path+"/config-validations", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		source, err := readConfig(w, r)
		if err != nil {
			writeError(w, err)
			return
		}
		issue, err := validator.Check(r.Context(), interfaceName(r), source)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Valid bool   `json:"valid"`
			Issue string `json:"issue,omitempty"`
		}{Valid: issue == lifecycle.ConfigIssueNone, Issue: issue.Error()})
	})
	if managed == nil {
		return
	}
	registerPeerDelta(router, path+"/peers", managed)
	router.Put(path+"/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		source, err := readConfig(w, r)
		if err != nil {
			writeError(w, err)
			return
		}
		var body struct {
			Peers     []peers.Input `json:"peers"`
			Reconcile bool          `json:"reconcile"`
		}
		if err := jsonv2.Unmarshal(source, &body, json.DefaultOptionsV1(), jsonv2.RejectUnknownMembers(true)); err != nil || body.Peers == nil {
			writeError(w, peers.ErrInvalid)
			return
		}
		result, err := managed.SyncPeers(r.Context(), interfaceName(r), body.Peers, body.Reconcile)
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	router.Put(path+"/execution", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		source, err := readConfig(w, r)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := managed.Start(r.Context(), interfaceName(r), source)
		if err != nil {
			writeError(w, err)
			return
		}
		writeStatus(w, result)
	})
	router.Delete(path+"/execution", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := managed.Stop(r.Context(), interfaceName(r)); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func readConfig(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	controller := http.NewResponseController(w)
	deadline := time.Now().Add(lifecycle.OperationTimeout)
	if requestDeadline, ok := r.Context().Deadline(); ok && requestDeadline.Before(deadline) {
		deadline = requestDeadline
	}
	deadlineSet := controller.SetReadDeadline(deadline) == nil
	source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, lifecycle.MaxConfigBytes))
	if err == nil {
		if deadlineSet {
			_ = controller.SetReadDeadline(time.Time{})
		}
		return source, nil
	}
	// An incomplete HTTP/1 body cannot be drained safely before replying.
	if r.ProtoMajor == 1 {
		w.Header().Set("Connection", "close")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, lifecycle.ErrTooLarge
	case errors.Is(err, os.ErrDeadlineExceeded):
		return nil, context.DeadlineExceeded
	default:
		if canceled := r.Context().Err(); canceled != nil {
			return nil, canceled
		}
		return nil, lifecycle.ErrInvalid
	}
}

func interfaceName(r *http.Request) string {
	name := chi.URLParam(r, "name")
	if r.URL.RawPath != "" {
		name, _ = url.PathUnescape(name)
	}
	return name
}

func writeStatus(w http.ResponseWriter, link status.Interface) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response{
		Name: link.Name, MTU: link.MTU, Up: link.Up, AppliedSHA256: link.AppliedSHA256,
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, peers.ErrInvalid):
		http.Error(w, peers.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, peers.ErrTooMany):
		http.Error(w, peers.ErrTooMany.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, peers.ErrUnconfirmed):
		http.Error(w, peers.ErrUnconfirmed.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, peers.ErrPrefixConflict), errors.Is(err, peers.ErrCapacity):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, lifecycle.ErrConflict):
		http.Error(w, lifecycle.ErrConflict.Error(), http.StatusConflict)
	case errors.Is(err, lifecycle.ErrInvalid):
		http.Error(w, lifecycle.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, lifecycle.ErrTooLarge):
		http.Error(w, lifecycle.ErrTooLarge.Error(), http.StatusRequestEntityTooLarge)
	case errors.Is(err, lifecycle.ErrUnconfirmed):
		http.Error(w, lifecycle.ErrUnconfirmed.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, status.ErrNotFound):
		http.Error(w, status.ErrNotFound.Error(), http.StatusNotFound)
	case errors.Is(err, status.ErrBusy):
		http.Error(w, status.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, context.Canceled):
		http.Error(w, "wireguard request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "wireguard status deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, status.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
