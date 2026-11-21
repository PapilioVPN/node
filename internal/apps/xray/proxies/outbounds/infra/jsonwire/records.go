// Package jsonwire owns private outbound envelopes shared by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
)

func DecodeID(data []byte) (string, error) {
	if len(data) > 1024 {
		return "", domain.ErrCapacity
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", domain.ErrInvalidArgument
	}
	key, err := decoder.Token()
	if err != nil || key != "instance_id" {
		return "", domain.ErrInvalidArgument
	}
	var id *string
	if err := decoder.Decode(&id); err != nil || id == nil {
		return "", domain.ErrInvalidArgument
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", domain.ErrInvalidArgument
	}
	return *id, nil
}
func Encode(result domain.Records) ([]byte, error) {
	type record struct {
		Tag           string `json:"tag"`
		Configuration []byte `json:"configuration"`
	}
	records := make([]record, len(result.Outbounds))
	for i, item := range result.Outbounds {
		records[i] = record{item.Tag, item.Configuration}
	}
	return json.Marshal(struct {
		InstanceID string    `json:"instance_id"`
		Generation uint64    `json:"generation,string"`
		Outbounds  []record  `json:"outbounds"`
		ObservedAt time.Time `json:"observed_at"`
		DurationNS int64     `json:"duration_ns,string"`
	}{result.InstanceID, result.Generation, records, result.ObservedAt, int64(result.Duration)})
}
