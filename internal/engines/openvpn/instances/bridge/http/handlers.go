package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
	"github.com/go-chi/chi/v5"
)

type handlers struct{ service *lifecycle.Service }

func (h handlers) status(w http.ResponseWriter, r *http.Request) {
	result, err := h.service.GetStatus(chi.URLParam(r, "id"))
	writeStatus(w, result, err)
}

func (h handlers) start(w http.ResponseWriter, r *http.Request) {
	var source []byte
	if r.ContentLength != 0 {
		var body struct {
			Config *string `json:"config_text"`
		}
		// Control characters may be encoded as six JSON bytes per input byte.
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6*lifecycle.MaxSourceBytes+4096))
		dec.DisallowUnknownFields()
		err := dec.Decode(&body)
		if err == nil {
			if err = dec.Decode(new(json.RawMessage)); err == io.EOF && body.Config != nil {
				source = []byte(*body.Config)
			} else {
				writeInputError(w, err)
				return
			}
		} else if err != io.EOF {
			writeInputError(w, err)
			return
		}
	}
	result, err := h.service.Start(r.Context(), chi.URLParam(r, "id"), source)
	writeStatus(w, result, err)
}

func (h handlers) stop(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		writeInputError(w, nil)
		return
	}
	result, err := h.service.Stop(r.Context(), chi.URLParam(r, "id"))
	writeStatus(w, result, err)
}

func (h handlers) validate(w http.ResponseWriter, r *http.Request) {
	var source []byte
	if r.ContentLength != 0 {
		var err error
		source, err = io.ReadAll(http.MaxBytesReader(w, r.Body, lifecycle.MaxSourceBytes))
		if err != nil {
			writeInputError(w, err)
			return
		}
	}
	result, err := h.service.Validate(r.Context(), chi.URLParam(r, "id"), source)
	if err != nil {
		writeStatus(w, lifecycle.Status{}, err)
		return
	}
	reply := validationResponse{ID: result.ID, Valid: result.Issue.Code == ""}
	if !reply.Valid {
		reply.Issue = &issueResponse{Code: result.Issue.Code, Line: result.Issue.Line}
	}
	writeJSON(w, http.StatusOK, reply)
}

func writeInputError(w http.ResponseWriter, err error) {
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		writeStatus(w, lifecycle.Status{}, lifecycle.ErrTooLarge)
		return
	}
	writeJSON(w, http.StatusBadRequest, failureResponse{Message: "invalid OpenVPN request body"})
}
