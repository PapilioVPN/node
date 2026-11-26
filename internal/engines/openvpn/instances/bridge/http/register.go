// Package http exposes the OpenVPN instance API over HTTP.
package http

import (
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *lifecycle.Service) {
	const path = "/v1/openvpn/instances/{id}"
	h := handlers{service: service}
	router.Get(path, h.status)
	router.Put(path+"/execution", h.start)
	router.Delete(path+"/execution", h.stop)
	router.Post(path+"/config-validations", h.validate)
	router.Get(path+"/sessions", h.sessions)
	router.Delete(path+"/sessions/{session_id}", h.disconnect)
	router.Get(path+"/logs", h.logs)
	router.Get(path+"/revocation-list", h.getRevocationList)
	router.Put(path+"/revocation-list", h.replaceRevocationList)
}
