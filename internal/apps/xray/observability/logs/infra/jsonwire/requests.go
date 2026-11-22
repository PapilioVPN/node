// Package jsonwire owns the shared HTTP and NATS log representation.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

const MaxRequestBytes = 4096

type Request struct {
	InstanceID       string
	Limit            uint32
	Cursor           string
	WaitMilliseconds uint32
}

func Decode(data []byte, follow bool) (Request, error) {
	var r Request
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return r, domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return r, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return r, domain.ErrInvalidArgument
		}
		seen[key] = true
		switch key {
		case "instance_id", "cursor":
			if key == "cursor" && !follow {
				return r, domain.ErrInvalidArgument
			}
			var value *string
			if err := d.Decode(&value); err != nil || value == nil {
				return r, domain.ErrInvalidArgument
			}
			if key == "instance_id" {
				r.InstanceID = *value
			} else {
				r.Cursor = *value
			}
		case "limit", "wait_ms":
			if key == "wait_ms" && !follow {
				return r, domain.ErrInvalidArgument
			}
			var value *uint32
			if err := d.Decode(&value); err != nil || value == nil {
				return r, domain.ErrInvalidArgument
			}
			if key == "limit" {
				r.Limit = *value
			} else {
				r.WaitMilliseconds = *value
			}
		default:
			return r, domain.ErrInvalidArgument
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return r, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || !seen["instance_id"] || follow && !seen["cursor"] {
		return r, domain.ErrInvalidArgument
	}
	return r, nil
}
