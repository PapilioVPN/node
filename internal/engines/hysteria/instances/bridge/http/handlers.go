// Package http exposes Hysteria operations on Node's authenticated HTTP router.
package http

import (
	"encoding/hex"
	"net/http"

	"github.com/PapilioVPN/node/internal/buildinfo"
	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	instances "github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *instances.Service) {
	router.Get(httpapi.InstancePath, func(w http.ResponseWriter, r *http.Request) {
		status, err := service.GetStatus(r.Context(), httpapi.ID(r))
		writeStatus(w, status, err)
	})
	router.Put(httpapi.InstancePath+"/execution", func(w http.ResponseWriter, r *http.Request) {
		source, err := httpapi.ReadBody(w, r, preflight.MaxBytes)
		if err != nil {
			httpapi.WriteError(w, err)
			return
		}
		status, err := service.Start(r.Context(), instances.ConfigRequest{ID: httpapi.ID(r), Source: source})
		writeStatus(w, status, err)
	})
	router.Delete(httpapi.InstancePath+"/execution", func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 {
			httpapi.WriteError(w, fault.ErrInvalid)
			return
		}
		status, err := service.Stop(r.Context(), httpapi.ID(r))
		writeStatus(w, status, err)
	})
}

type statusResponse struct {
	ID                  string `json:"id"`
	State               string `json:"state"`
	HysteriaVersion     string `json:"hysteria_version,omitempty"`
	NodeVersion         string `json:"node_version"`
	StartAction         string `json:"start_action,omitempty"`
	AppliedConfigSHA256 string `json:"applied_config_sha256,omitempty"`
}

var stateNames = [...]string{"", "stopped", "starting", "ready", "stopping", "failed"}
var actionNames = [...]string{"", "started", "heartbeat", "restarted", "recovered"}

func writeStatus(w http.ResponseWriter, s instances.Status, err error) {
	if s.ID == "" && err != nil {
		httpapi.WriteError(w, err)
		return
	}
	result := statusResponse{ID: s.ID, State: stateNames[s.State], HysteriaVersion: s.Version, NodeVersion: buildinfo.Version, StartAction: actionNames[s.Action]}
	if s.Digest != nil {
		result.AppliedConfigSHA256 = hex.EncodeToString(s.Digest[:])
	}
	if err != nil {
		code, message := httpapi.ErrorStatus(err)
		httpapi.WriteJSON(w, code, struct {
			Message string         `json:"message"`
			Status  statusResponse `json:"status"`
		}{message, result})
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, result)
}
