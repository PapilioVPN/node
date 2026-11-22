// Package jsonwire owns strict bounded JSON input for instance recovery APIs.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type Input struct {
	ID               string `json:"instance_id"`
	After            string `json:"after"`
	Limit            uint32 `json:"limit"`
	ExecutionID      string `json:"execution_id"`
	ExpectedRevision string `json:"expected_revision"`
	Owner            string `json:"owner"`
	TTLSeconds       uint32 `json:"ttl_seconds"`
	Policy           string `json:"policy"`
	Token            string `json:"token"`
	Preserve         bool   `json:"preserve"`
	MaxAttempts      uint32 `json:"max_attempts"`
	ReplaceUnready   bool   `json:"replace_unready"`
}

// Decode rejects duplicate keys, unknown keys, null and missing required fields.
// Allowed fields are required except the explicit optional names in optional.
func Decode(data []byte, fields, optional string) (Input, error) {
	var result Input
	if len(data) > 2048 {
		return result, domain.ErrInvalidArgument
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields(fields) {
		allowed[key] = true
	}
	seen := map[string]bool{}
	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return result, domain.ErrInvalidArgument
	}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || !allowed[name] || seen[name] {
			return result, domain.ErrInvalidArgument
		}
		seen[name] = true
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return result, domain.ErrInvalidArgument
		}
	}
	if last, err := d.Token(); err != nil || last != json.Delim('}') {
		return result, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return result, domain.ErrInvalidArgument
	}
	for _, key := range strings.Fields(optional) {
		delete(allowed, key)
	}
	for key := range allowed {
		if !seen[key] {
			return result, domain.ErrInvalidArgument
		}
	}
	if json.Unmarshal(data, &result) != nil {
		return result, domain.ErrInvalidArgument
	}
	return result, nil
}
func Revision(text string) (uint64, error) {
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(value, 10) != text {
		return 0, domain.ErrInvalidArgument
	}
	return value, nil
}
