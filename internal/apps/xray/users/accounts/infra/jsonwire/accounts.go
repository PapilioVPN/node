// Package jsonwire owns the account observation envelopes used by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type Selection struct {
	InstanceID string
	InboundTag string
	Email      *string
}

func DecodeSelection(data []byte) (selection Selection, err error) {
	if len(data) > 16<<10 {
		return selection, domain.ErrCapacity
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return selection, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return selection, domain.ErrInvalidArgument
		}
		seen[name] = true
		var value *string
		switch name {
		case "instance_id", "inbound_tag", "email":
			if err := decoder.Decode(&value); err != nil || value == nil {
				return selection, domain.ErrInvalidArgument
			}
		default:
			return selection, domain.ErrInvalidArgument
		}
		switch name {
		case "instance_id":
			selection.InstanceID = *value
		case "inbound_tag":
			selection.InboundTag = *value
		case "email":
			selection.Email = value
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return selection, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return selection, domain.ErrInvalidArgument
	}
	if !seen["instance_id"] || !seen["inbound_tag"] {
		return selection, domain.ErrInvalidArgument
	}
	return selection, nil
}

type observation struct {
	InstanceID string    `json:"instance_id"`
	InboundTag string    `json:"inbound_tag"`
	Generation uint64    `json:"generation,string"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
}

func metadata(value domain.Observation) observation {
	return observation{value.InstanceID, value.InboundTag, value.Generation, value.ObservedAt, int64(value.Duration)}
}
func EncodeCount(result domain.Count) ([]byte, error) {
	return json.Marshal(struct {
		Observation observation `json:"observation"`
		Count       int64       `json:"count,string"`
	}{metadata(result.Observation), result.Count})
}
func EncodeAccount(result domain.Account) ([]byte, error) {
	type account struct {
		Email         string `json:"email"`
		Level         uint32 `json:"level"`
		Configuration []byte `json:"configuration"`
	}
	var user *account
	if result.User != nil {
		user = &account{result.User.Email, result.User.Level, result.User.Configuration}
	}
	return json.Marshal(struct {
		Observation    observation `json:"observation"`
		RequestedEmail string      `json:"requested_email"`
		User           *account    `json:"user"`
	}{metadata(result.Observation), result.RequestedEmail, user})
}
