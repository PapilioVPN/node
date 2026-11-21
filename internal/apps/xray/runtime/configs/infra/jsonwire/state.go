package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

// StateRequest preserves field presence so revision zero cannot mean omission.
type StateRequest struct {
	InstanceID       *string
	ExpectedRevision *uint64
	Sources          []domain.Source
	HasSources       bool
}

func DecodeState(data []byte) (result StateRequest, err error) {
	if len(data) > 2<<20 {
		return result, domain.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return result, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return result, domain.ErrInvalidArgument
		}
		name, ok := key.(string)
		if !ok || seen[name] {
			return result, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			var id *string
			if err := decoder.Decode(&id); err != nil || id == nil {
				return result, domain.ErrInvalidArgument
			}
			result.InstanceID = id
		case "expected_revision":
			var text *string
			if err := decoder.Decode(&text); err != nil || text == nil {
				return result, domain.ErrInvalidArgument
			}
			value, err := strconv.ParseUint(*text, 10, 64)
			if err != nil || strconv.FormatUint(value, 10) != *text {
				return result, domain.ErrInvalidArgument
			}
			result.ExpectedRevision = &value
		case "sources":
			result.HasSources = true
			result.Sources, err = DecodeSources(decoder)
			if err != nil {
				return result, err
			}
		default:
			return result, domain.ErrInvalidArgument
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return result, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return result, domain.ErrInvalidArgument
	}
	return result, nil
}

// DecodeSources reads one bounded native source collection from a JSON decoder.
func DecodeSources(decoder *json.Decoder) (sources []domain.Source, err error) {
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, domain.ErrInvalidArgument
	}
	for decoder.More() {
		if len(sources) >= domain.MaxSources {
			return nil, domain.ErrTooLarge
		}
		source, err := decodeSource(decoder)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, domain.ErrInvalidArgument
	}
	return sources, nil
}
