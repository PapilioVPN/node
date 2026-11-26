package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

func (h handlers) getRevocationList(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.ReadRevocationList(chi.URLParam(r, "id"))
	writeRevocationResult(w, result, err)
}

func (h handlers) replaceRevocationList(w http.ResponseWriter, r *http.Request) {
	source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, lifecycle.MaxRevocationBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeStatus(w, lifecycle.Status{}, lifecycle.ErrTooLarge)
		} else {
			writeInputError(w, err)
		}
		return
	}
	result, err := h.service.ReplaceRevocationList(r.Context(), chi.URLParam(r, "id"), source)
	writeRevocationResult(w, result, err)
}

func writeRevocationResult(w http.ResponseWriter, result lifecycle.RevocationStatus, err error) {
	if err != nil {
		writeStatus(w, lifecycle.Status{}, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
