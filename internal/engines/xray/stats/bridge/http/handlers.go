package http

import (
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/runtime"
	"github.com/go-chi/chi/v5"
)

func getRuntimeStats(service *runtime.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := service.GetRuntimeStats(r.Context(), chi.URLParam(r, "id"))
		writeResult(w, result, err)
	}
}
