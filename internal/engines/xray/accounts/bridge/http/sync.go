package http

import (
	"encoding/json"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
)

func syncUser(service *membership.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input accountInput
		if !readBody(w, r, &input) {
			return
		}
		account, err := input.account()
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.SyncUser(r.Context(), membership.SyncUserRequest{
			Scope: scope(r), Email: account.Email, UUID: account.UUID, Flow: account.Flow, Credential: account.Credential,
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
			Accounts []accountInput `json:"accounts"`
		}
		if !readLimitedBody(w, r, &input, 2<<20) {
			return
		}
		accounts, err := decodeAccounts(input.Accounts)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.SyncUsers(r.Context(), membership.SyncUsersRequest{Scope: scope(r), Accounts: accounts})
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

type accountInput struct {
	Email      string                 `json:"email"`
	UUID       json.RawMessage        `json:"uuid"`
	Flow       *string                `json:"flow"`
	Credential *membership.Credential `json:"credential"`
}

func (input accountInput) account() (membership.AccountInput, error) {
	uuid, err := optionalUUID(input.UUID)
	return membership.AccountInput{Email: input.Email, UUID: uuid, Flow: input.Flow, Credential: input.Credential}, err
}

func decodeAccounts(inputs []accountInput) ([]membership.AccountInput, error) {
	if inputs == nil || len(inputs) > membership.MaxSyncAccounts {
		return nil, membership.ErrInvalid
	}
	result := make([]membership.AccountInput, len(inputs))
	for i, input := range inputs {
		account, err := input.account()
		if err != nil {
			return nil, err
		}
		result[i] = account
	}
	return result, nil
}
