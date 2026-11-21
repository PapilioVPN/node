// Package jsonwire owns configuration envelopes shared by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func DecodeCompilation(data []byte) (id string, sources []domain.Source, err error) {
	if len(data) > 2<<20 {
		return "", nil, domain.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", nil, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return "", nil, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return "", nil, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			if err := decoder.Decode(&id); err != nil {
				return "", nil, domain.ErrInvalidArgument
			}
		case "sources":
			sources, err = DecodeSources(decoder)
			if err != nil {
				return "", nil, err
			}
		default:
			return "", nil, domain.ErrInvalidArgument
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(seen) != 2 {
		return "", nil, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", nil, domain.ErrInvalidArgument
	}
	return id, sources, nil
}

func decodeSource(decoder *json.Decoder) (source domain.Source, err error) {
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return source, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return source, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return source, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "name":
			err = decoder.Decode(&source.Name)
		case "content":
			err = decoder.Decode(&source.Content)
		default:
			return source, domain.ErrInvalidArgument
		}
		if err != nil {
			return source, domain.ErrInvalidArgument
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || len(seen) != 2 {
		return source, domain.ErrInvalidArgument
	}
	return source, nil
}

func EncodeCompilation(result domain.Compilation) ([]byte, error) {
	return json.Marshal(struct {
		InstanceID string    `json:"instance_id"`
		Content    []byte    `json:"content"`
		SHA256     string    `json:"sha256"`
		PreparedAt time.Time `json:"prepared_at"`
		DurationNS string    `json:"duration_ns"`
	}{result.InstanceID, result.Content, result.SHA256, result.PreparedAt, strconv.FormatInt(int64(result.Duration), 10)})
}
