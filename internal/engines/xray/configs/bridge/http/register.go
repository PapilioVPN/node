// Package http exposes Xray config validation over Node's HTTP API.
package http

import (
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"github.com/go-chi/chi/v5"
)

func Register(router chi.Router, service *validation.Service) {
	router.Post("/v1/xray/instances/{id}/config-validations", validate(service))
}
