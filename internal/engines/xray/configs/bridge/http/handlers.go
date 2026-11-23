package http

import (
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"github.com/go-chi/chi/v5"
)

func validate(service *validation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != 0 {
			http.Error(w, "request body must be empty", http.StatusBadRequest)
			return
		}
		result, err := service.Validate(r.Context(), chi.URLParam(r, "id"))
		writeResult(w, result, err)
	}
}
