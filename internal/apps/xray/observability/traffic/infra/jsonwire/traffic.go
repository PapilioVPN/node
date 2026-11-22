// Package jsonwire owns the exact traffic JSON request and observation contract.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

const MaxRequestBytes = 32 << 10

// DecodeRequest rejects missing, duplicate, unknown and non-string fields.
func DecodeRequest(data []byte) (string, domain.Selection, error) {
	invalid := func() (string, domain.Selection, error) { return "", domain.Selection{}, domain.ErrInvalidArgument }
	if len(data) > MaxRequestBytes || !utf8.Valid(data) {
		return invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return invalid()
	}
	fields := make(map[string]string, 4)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok {
			return invalid()
		}
		switch key {
		case "instance_id", "scope", "name", "direction":
		default:
			return invalid()
		}
		if _, exists := fields[key]; exists {
			return invalid()
		}
		value, err := decoder.Token()
		if err != nil {
			return invalid()
		}
		text, ok := value.(string)
		if !ok {
			return invalid()
		}
		fields[key] = text
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(fields) != 4 {
		return invalid()
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return invalid()
	}
	return fields["instance_id"], domain.Selection{Scope: domain.Scope(fields["scope"]), Name: fields["name"], Direction: domain.Direction(fields["direction"])}, nil
}

func Encode(result domain.Observation) ([]byte, error) {
	return json.Marshal(struct {
		InstanceID string           `json:"instance_id"`
		Generation uint64           `json:"generation,string"`
		Scope      domain.Scope     `json:"scope"`
		Name       string           `json:"name"`
		Direction  domain.Direction `json:"direction"`
		Bytes      int64            `json:"bytes,string"`
		Source     string           `json:"source"`
		ObservedAt time.Time        `json:"observed_at"`
		DurationNS int64            `json:"duration_ns,string"`
	}{result.InstanceID, result.Generation, result.Selection.Scope, result.Selection.Name, result.Selection.Direction, result.Bytes, result.Source, result.ObservedAt, int64(result.Duration)})
}
