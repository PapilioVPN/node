// Package http exposes Xray runtime statistics over Node's HTTP API.
package http

import (
	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *runtime.Service) {
	router.Get("/v1/xray/instances/{id}/stats/runtime", getRuntimeStats(service))
}
