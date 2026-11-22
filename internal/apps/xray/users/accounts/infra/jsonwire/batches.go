package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type AdditionsRequest struct {
	Parallelism int
	InstanceID  *string
	Items       []domain.Addition
}
type RemovalsRequest struct {
	Parallelism int
	InstanceID  *string
	Items       []domain.Removal
}

func DecodeAdditions(data []byte) (result AdditionsRequest, err error) {
	id, items, parallelism, err := decodeBatch(data)
	if err != nil {
		return result, err
	}
	result.InstanceID = id
	result.Parallelism = parallelism
	for _, item := range items {
		if item.InstanceID != nil || item.InboundTag == nil || item.Email != nil || item.HasSources || !item.HasConfiguration {
			return AdditionsRequest{}, domain.ErrInvalidArgument
		}
		result.Items = append(result.Items, domain.Addition{InboundTag: *item.InboundTag, Configuration: item.Configuration})
	}
	return result, nil
}
func DecodeRemovals(data []byte) (result RemovalsRequest, err error) {
	id, items, parallelism, err := decodeBatch(data)
	if err != nil {
		return result, err
	}
	result.InstanceID = id
	result.Parallelism = parallelism
	for _, item := range items {
		if item.InstanceID != nil || item.InboundTag == nil || item.Email == nil || item.HasSources || item.HasConfiguration {
			return RemovalsRequest{}, domain.ErrInvalidArgument
		}
		result.Items = append(result.Items, domain.Removal{InboundTag: *item.InboundTag, Email: *item.Email})
	}
	return result, nil
}

func decodeBatch(data []byte) (id *string, items []ControlRequest, parallelism int, err error) {
	if len(data) > 8<<20 {
		return nil, nil, 0, domain.ErrCapacity
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, nil, 0, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return nil, nil, 0, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			if err := decoder.Decode(&id); err != nil || id == nil {
				return nil, nil, 0, domain.ErrInvalidArgument
			}
		case "parallelism":
			var value *int
			if err := decoder.Decode(&value); err != nil || value == nil || *value < 0 || *value > domain.MaxBatchParallelism {
				return nil, nil, 0, domain.ErrInvalidArgument
			}
			parallelism = *value
		case "items":
			if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
				return nil, nil, 0, domain.ErrInvalidArgument
			}
			for decoder.More() {
				if len(items) == domain.MaxBatchItems {
					return nil, nil, 0, domain.ErrCapacity
				}
				item, err := decodeControlObject(decoder)
				if err != nil {
					return nil, nil, 0, err
				}
				items = append(items, item)
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
				return nil, nil, 0, domain.ErrInvalidArgument
			}
		default:
			return nil, nil, 0, domain.ErrInvalidArgument
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seen["items"] || len(items) == 0 {
		return nil, nil, 0, domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, nil, 0, domain.ErrInvalidArgument
	}
	return id, items, parallelism, nil
}

// EncodeBatch takes boundary-owned public codes; native private causes are never serialized.
func EncodeBatch(result domain.Batch, codes []string, code string) ([]byte, error) {
	if len(codes) != len(result.Items) {
		return nil, domain.ErrInvalidArgument
	}
	type item struct {
		Index     int       `json:"index"`
		Attempted bool      `json:"attempted"`
		Operation operation `json:"operation"`
		Error     string    `json:"error,omitempty"`
	}
	items := make([]item, len(result.Items))
	for i, value := range result.Items {
		items[i] = item{value.Index, value.Attempted, operationResult(value.Operation), codes[i]}
	}
	return json.Marshal(struct {
		Operation operation `json:"operation"`
		Items     []item    `json:"items"`
		Error     string    `json:"error,omitempty"`
	}{operationResult(result.Operation), items, code})
}
