package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
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

type startBody struct {
	Config           json.RawMessage `json:"config"`
	InitialUsers     []inboundUsers  `json:"initial_users"`
	ExcludedInbounds []string        `json:"exclude_inbounds"`
	KeepAliveSeconds *uint32         `json:"keep_alive_seconds"`
}

type inboundUsers struct {
	InboundTag string                    `json:"inbound_tag"`
	Accounts   []membership.AccountInput `json:"accounts"`
}

func (h handlers) start(w http.ResponseWriter, r *http.Request) {
	input := lifecycle.StartRequest{ID: chi.URLParam(r, "id")}
	if r.ContentLength != 0 {
		body, err := decodeStart(w, r)
		switch {
		case errors.Is(err, io.EOF):
		case err != nil:
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "xray start request body is too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, lifecycle.ErrInvalidStart.Error(), http.StatusBadRequest)
			}
			return
		default:
			input.ConfigJSON = body.Config
			input.ExcludedInbounds = body.ExcludedInbounds
			input.KeepAliveSeconds = body.KeepAliveSeconds
			for _, group := range body.InitialUsers {
				input.InitialUsers = append(input.InitialUsers, lifecycle.InboundUsers(group))
			}
		}
	}
	status, err := h.service.StartWith(r.Context(), input)
	writeResult(w, status, err)
}

func decodeStart(w http.ResponseWriter, r *http.Request) (startBody, error) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	var body startBody
	if err := decoder.Decode(&body); err != nil {
		return startBody{}, err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		if err == nil {
			err = lifecycle.ErrInvalidStart
		}
		return startBody{}, err
	}
	if (body.Config == nil && body.InitialUsers == nil && body.ExcludedInbounds == nil && body.KeepAliveSeconds == nil) ||
		(body.Config != nil && body.Config[0] != '{') {
		return startBody{}, lifecycle.ErrInvalidStart
	}
	return body, nil
}

func (h handlers) stop(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		http.Error(w, "request body must be empty", http.StatusBadRequest)
		return
	}
	status, err := h.service.Stop(r.Context(), chi.URLParam(r, "id"))
	writeResult(w, status, err)
}
