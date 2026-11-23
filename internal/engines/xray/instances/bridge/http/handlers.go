package http

import (
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

type handlers struct {
	service *lifecycle.Service
}

func (h handlers) getStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.service.GetStatus(chi.URLParam(r, "id"))
	writeResult(w, status, err)
}

func (h handlers) start(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		http.Error(w, "request body must be empty", http.StatusBadRequest)
		return
	}
	status, err := h.service.Start(r.Context(), chi.URLParam(r, "id"))
	writeResult(w, status, err)
}

func (h handlers) stop(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		http.Error(w, "request body must be empty", http.StatusBadRequest)
		return
	}
	status, err := h.service.Stop(r.Context(), chi.URLParam(r, "id"))
	writeResult(w, status, err)
}
