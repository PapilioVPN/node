package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	sourcewire "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/infra/jsonwire"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type PreparationRequest struct {
	InstanceID *string
	Sources    []configs.Source
	Inbounds   []domain.InboundUserSet
}

func DecodePreparation(data []byte) (request PreparationRequest, err error) {
	if len(data) > 48<<20 {
		return request, domain.ErrCapacity
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return request, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	remainingUsers, remainingBytes := domain.MaxConfigurationUsers, configs.MaxCompiledBytes
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return request, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "instance_id":
			if err := d.Decode(&request.InstanceID); err != nil || request.InstanceID == nil {
				return request, domain.ErrInvalidArgument
			}
		case "sources":
			request.Sources, err = sourcewire.DecodeSources(d)
			if errors.Is(err, configs.ErrTooLarge) {
				return request, domain.ErrCapacity
			}
			if err != nil {
				return request, domain.ErrInvalidArgument
			}
		case "inbounds":
			if token, err := d.Token(); err != nil || token != json.Delim('[') {
				return request, domain.ErrInvalidArgument
			}
			for d.More() {
				if len(request.Inbounds) >= domain.MaxConfigurationUsers {
					return request, domain.ErrCapacity
				}
				set, err := decodeUserSet(d, &remainingUsers, &remainingBytes)
				if err != nil {
					return request, err
				}
				request.Inbounds = append(request.Inbounds, set)
			}
			if token, err := d.Token(); err != nil || token != json.Delim(']') {
				return request, domain.ErrInvalidArgument
			}
		default:
			return request, domain.ErrInvalidArgument
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || !seen["sources"] || !seen["inbounds"] {
		return request, domain.ErrInvalidArgument
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return request, domain.ErrInvalidArgument
	}
	return request, nil
}

func decodeUserSet(d *json.Decoder, remainingUsers, remainingBytes *int) (set domain.InboundUserSet, err error) {
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return set, domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return set, domain.ErrInvalidArgument
		}
		seen[name] = true
		switch name {
		case "inbound_tag":
			var tag *string
			if err := d.Decode(&tag); err != nil || tag == nil {
				return set, domain.ErrInvalidArgument
			}
			set.InboundTag = *tag
		case "accounts":
			if token, err := d.Token(); err != nil || token != json.Delim('[') {
				return set, domain.ErrInvalidArgument
			}
			for d.More() {
				if *remainingUsers <= 0 {
					return set, domain.ErrCapacity
				}
				var raw []byte
				if err := d.Decode(&raw); err != nil || len(raw) == 0 {
					return set, domain.ErrInvalidArgument
				}
				if len(raw) > domain.MaxAccountBytes || len(raw) > *remainingBytes {
					return set, domain.ErrCapacity
				}
				*remainingUsers--
				*remainingBytes -= len(raw)
				set.Accounts = append(set.Accounts, raw)
			}
			if token, err := d.Token(); err != nil || token != json.Delim(']') {
				return set, domain.ErrInvalidArgument
			}
		default:
			return set, domain.ErrInvalidArgument
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(seen) != 2 {
		return set, domain.ErrInvalidArgument
	}
	return set, nil
}
