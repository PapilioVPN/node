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
}
