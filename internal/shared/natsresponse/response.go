// Package natsresponse encodes bounded JSON replies without exposing native causes.
package natsresponse

import (
	"encoding/json"
	"errors"

	"github.com/nats-io/nats.go"
)

type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Error(message *nats.Msg, code, description string) error {
	payload, err := json.Marshal(struct {
		Error failure `json:"error"`
	}{failure{code, description}})
	if err != nil {
		return err
	}
	return message.Respond(payload)
}

// JSON returns a complete result or an explicit limit error, never a partial success.
// The payload ceiling is checked before publish. Broker limits may be smaller.
func JSON(message *nats.Msg, value any, maxBytes int) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return Error(message, "internal", "cannot encode response")
	}
	if len(payload) > maxBytes {
		return Error(message, "resource_exhausted", "response exceeds configured byte limit")
	}
	err = message.Respond(payload)
	if errors.Is(err, nats.ErrMaxPayload) {
		return Error(message, "resource_exhausted", "response exceeds broker byte limit")
	}
	return err
}
