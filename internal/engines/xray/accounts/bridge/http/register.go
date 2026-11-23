package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/go-chi/chi/v5"
)

type addAccount struct {
	Email string `json:"email"`
	UUID  string `json:"uuid"`
}

type removeAccount struct {
	Email string `json:"email"`
}

type accountCount struct {
	Count int64 `json:"count"`
}

func Register(router chi.Router, service *membership.Service) {
	const base = "/v1/xray/instances/{id}/inbounds/{tag}/accounts"
	router.Put(base, syncUsers(service))
	router.Put(base+"/membership", syncUser(service))
	router.Put(base+"/membership/stream", syncUsersChunked(service))
	const instance = "/v1/xray/instances/{id}/accounts"
	router.Put(instance, syncUsers(service))
	router.Put(instance+"/membership", syncUser(service))
	router.Put(instance+"/membership/stream", syncUsersChunked(service))
	router.Post(base, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input addAccount
		if !readBody(w, r, &input) {
			return
		}
		err := service.Add(r.Context(), membership.AddRequest{Scope: scope(r), Email: input.Email, UUID: input.UUID})
		writeMutation(w, err)
	})
	router.Delete(base, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input removeAccount
		if !readBody(w, r, &input) {
			return
		}
		err := service.Remove(r.Context(), membership.RemoveRequest{Scope: scope(r), Email: input.Email})
		writeMutation(w, err)
	})
	router.Get(base+"/count", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		count, err := service.Count(r.Context(), scope(r))
		if err != nil {
			writeError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(accountCount{Count: count})
	})
}

func scope(r *http.Request) membership.Scope {
	id, tag := chi.URLParam(r, "id"), chi.URLParam(r, "tag")
	if r.URL.RawPath != "" {
		id, _ = url.PathUnescape(id)
		tag, _ = url.PathUnescape(tag)
	}
	return membership.Scope{InstanceID: id, InboundTag: tag}
}

func readBody(w http.ResponseWriter, r *http.Request, target any) bool {
	return readLimitedBody(w, r, target, 8<<10)
}

func readLimitedBody(w http.ResponseWriter, r *http.Request, target any, limitBytes int64) bool {
	reader := http.MaxBytesReader(w, r.Body, limitBytes)
	defer reader.Close()
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(target)
	if err == nil {
		var extra json.RawMessage
		if err = decoder.Decode(&extra); err == io.EOF {
			return true
		}
	}
	var limit *http.MaxBytesError
	if errors.As(err, &limit) {
		http.Error(w, "Xray account request is too large", http.StatusRequestEntityTooLarge)
	} else {
		writeError(w, membership.ErrInvalid)
	}
	return false
}

func writeMutation(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, membership.ErrInvalid):
		http.Error(w, membership.ErrInvalid.Error(), http.StatusBadRequest)
	case errors.Is(err, membership.ErrInstanceMissing), errors.Is(err, membership.ErrInboundMissing), errors.Is(err, membership.ErrAccountMissing):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, membership.ErrProtocol), errors.Is(err, membership.ErrExists), errors.Is(err, membership.ErrAmbiguous),
		errors.Is(err, membership.ErrUnknownEffect), errors.Is(err, membership.ErrFenced):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, membership.ErrBusy):
		http.Error(w, membership.ErrBusy.Error(), http.StatusTooManyRequests)
	case errors.Is(err, membership.ErrTooLarge):
		http.Error(w, membership.ErrTooLarge.Error(), http.StatusUnprocessableEntity)
	case errors.Is(err, context.Canceled):
		http.Error(w, "Xray account request canceled", http.StatusRequestTimeout)
	case errors.Is(err, context.DeadlineExceeded):
		http.Error(w, "Xray account request deadline exceeded", http.StatusGatewayTimeout)
	default:
		http.Error(w, membership.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
