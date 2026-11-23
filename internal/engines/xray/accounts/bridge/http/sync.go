package http

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
)

func syncUser(service *membership.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		var input struct {
			accountInput
			InboundTags []string `json:"inbound_tags"`
		}
		inbound := scope(r)
		var limit int64 = 8 << 10
		if inbound.InboundTag == "" {
			limit = 160 << 10
		}
		if !readLimitedBody(w, r, &input, limit) {
			return
		}
		account, err := input.account()
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.SyncUser(r.Context(), membership.SyncUserRequest{
			Scope: inbound, InboundTags: input.InboundTags, Email: account.Email, UUID: account.UUID, Flow: account.Flow, Credential: account.Credential, Assignments: account.Assignments,
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
			Accounts    []accountInput `json:"accounts"`
			InboundTags []string       `json:"inbound_tags"`
		}
		if !readLimitedBody(w, r, &input, 2<<20) {
			return
		}
		accounts, err := decodeAccounts(input.Accounts)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := service.SyncUsers(r.Context(), membership.SyncUsersRequest{Scope: scope(r), InboundTags: input.InboundTags, Accounts: accounts})
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
	Email       string          `json:"email"`
	UUID        json.RawMessage `json:"uuid"`
	Flow        json.RawMessage `json:"flow"`
	Credential  json.RawMessage `json:"credential"`
	Assignments []struct {
		InboundTag string                 `json:"inbound_tag"`
		Credential *membership.Credential `json:"credential"`
	} `json:"assignments"`
}

func (input accountInput) account() (membership.AccountInput, error) {
	uuid, err := optionalUUID(input.UUID)
	flow, flowErr := optionalUUID(input.Flow)
	account := membership.AccountInput{Email: input.Email, UUID: uuid, Flow: flow}
	if err != nil || flowErr != nil || len(input.Assignments) > membership.MaxSyncInbounds {
		return account, membership.ErrInvalid
	}
	if input.Credential != nil {
		decoder := json.NewDecoder(bytes.NewReader(input.Credential))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&account.Credential); err != nil || account.Credential == nil {
			return account, membership.ErrInvalid
		}
	}
	for _, assignment := range input.Assignments {
		if assignment.Credential == nil {
			return account, membership.ErrInvalid
		}
		account.Assignments = append(account.Assignments, membership.Assignment{InboundTag: assignment.InboundTag, Credential: *assignment.Credential})
	}
	return account, nil
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
