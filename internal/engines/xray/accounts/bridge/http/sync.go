package http

import (
	"encoding/json"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
)

func syncUser(service *membership.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Email string          `json:"email"`
			UUID  json.RawMessage `json:"uuid"`
		}
		if !readBody(w, r, &input) {
			return
		}
		uuid, err := optionalUUID(input.UUID)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.SyncUser(r.Context(), membership.SyncUserRequest{
			Scope: scope(r), Email: input.Email, UUID: uuid,
		})
		writeSync(w, result, err)
	}
}

func writeSync(w http.ResponseWriter, result membership.SyncResult, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func syncUsers(service *membership.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			Accounts []membership.AccountInput `json:"accounts"`
		}
		if !readLimitedBody(w, r, &input, 2<<20) {
			return
		}
		result, err := service.SyncUsers(r.Context(), membership.SyncUsersRequest{Scope: scope(r), Accounts: input.Accounts})
		writeSync(w, result, err)
	}
}

func optionalUUID(raw json.RawMessage) (*string, error) {
	if raw == nil {
		return nil, nil
	}
	var uuid *string
	if err := json.Unmarshal(raw, &uuid); err != nil || uuid == nil {
		return nil, membership.ErrInvalid
	}
	return uuid, nil
}
