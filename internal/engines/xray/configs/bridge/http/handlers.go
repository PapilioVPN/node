package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	"github.com/PapilioVPN/node/internal/engines/xray/configs/validation"
	"github.com/go-chi/chi/v5"
)

func validate(service *validation.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		request := validation.Request{ID: chi.URLParam(r, "id")}
		if r.ContentLength > runconfig.MaxSourceBytes {
			writeResult(w, validation.Result{}, runconfig.ErrSourceTooLarge)
			return
		}
		if r.ContentLength != 0 {
			source, err := io.ReadAll(http.MaxBytesReader(w, r.Body, runconfig.MaxSourceBytes))
			if err != nil {
				var tooLarge *http.MaxBytesError
				switch {
				case errors.As(err, &tooLarge):
					writeResult(w, validation.Result{}, runconfig.ErrSourceTooLarge)
				case r.Context().Err() != nil:
					writeResult(w, validation.Result{}, r.Context().Err())
				default:
					http.Error(w, "could not read xray configuration request", http.StatusBadRequest)
				}
				return
			}
			request.ConfigJSON = source
		}
		result, err := service.Validate(r.Context(), request)
		writeResult(w, result, err)
	}
}
