// Package http exposes the Xray instance lifecycle over Node's HTTP API.
package http

import (
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *lifecycle.Service) {
	const instance = "/v1/xray/instances/{id}"
	handler := handlers{service: service}
	router.Get(instance, handler.getStatus)
	router.Put(instance+"/execution", handler.start)
	router.Delete(instance+"/execution", handler.stop)
}
