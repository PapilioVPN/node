// Package jsonwire owns strict credential envelopes shared by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func object(data []byte, externalID string, field func(string, *json.Decoder) error) (string, error) {
	if len(data) > domain.MaxRequestBytes {
		return "", domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return "", domain.ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", domain.ErrInvalidArgument
	}
	id := externalID
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		if err != nil || !ok || seen[name] {
			return "", domain.ErrInvalidArgument
		}
		seen[name] = true
		if name == "instance_id" {
			if externalID != "" {
				return "", domain.ErrInvalidArgument
			}
			var value *string
			if stringValue(decoder, &value) != nil {
				return "", domain.ErrInvalidArgument
			}
			id = *value
		} else if err := field(name, decoder); err != nil {
			return "", err
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) || (externalID == "" && !seen["instance_id"]) {
		return "", domain.ErrInvalidArgument
	}
	return id, nil
}
func stringValue(d *json.Decoder, target **string) error {
	if err := d.Decode(target); err != nil || *target == nil {
		return domain.ErrInvalidArgument
	}
	return nil
}
func noField(string, *json.Decoder) error { return domain.ErrInvalidArgument }
func DecodeUUID(data []byte, id string) (string, domain.UUIDInput, error) {
	var input domain.UUIDInput
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		if name != "name" {
			return domain.ErrInvalidArgument
		}
		return stringValue(d, &input.Name)
	})
	return target, input, err
}
func DecodeX25519(data []byte, id string) (string, domain.X25519Input, error) {
	var input domain.X25519Input
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		switch name {
		case "private_key":
			return stringValue(d, &input.PrivateKey)
		case "encoding":
			var value *string
			if stringValue(d, &value) != nil {
				return domain.ErrInvalidArgument
			}
			input.Encoding = domain.Encoding(*value)
			return nil
		default:
			return domain.ErrInvalidArgument
		}
	})
	return target, input, err
}
func DecodeWireGuard(data []byte, id string) (string, domain.WireGuardInput, error) {
	var input domain.WireGuardInput
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		if name != "private_key" {
			return domain.ErrInvalidArgument
		}
		return stringValue(d, &input.PrivateKey)
	})
	return target, input, err
}
func DecodeSeed(data []byte, id string) (string, domain.SeedInput, error) {
	var input domain.SeedInput
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		if name != "seed" {
			return domain.ErrInvalidArgument
		}
		return stringValue(d, &input.Seed)
	})
	return target, input, err
}
func DecodeEmpty(data []byte, id string) (string, error) { return object(data, id, noField) }
func DecodeCertificate(data []byte, id string) (string, domain.CertificateInput, error) {
	var input domain.CertificateInput
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		switch name {
		case "domains":
			if token, err := d.Token(); err != nil || token != json.Delim('[') {
				return domain.ErrInvalidArgument
			}
			input.Domains = []string{}
			for d.More() {
				if len(input.Domains) >= domain.MaxDomains {
					return domain.ErrInvalidArgument
				}
				var value *string
				if stringValue(d, &value) != nil {
					return domain.ErrInvalidArgument
				}
				input.Domains = append(input.Domains, *value)
			}
			if token, err := d.Token(); err != nil || token != json.Delim(']') {
				return domain.ErrInvalidArgument
			}
			return nil
		case "common_name":
			return stringValue(d, &input.CommonName)
		case "organization":
			return stringValue(d, &input.Organization)
		case "authority":
			var value *bool
			if d.Decode(&value) != nil || value == nil {
				return domain.ErrInvalidArgument
			}
			input.Authority = *value
			return nil
		case "valid_for_ns":
			var value *string
			if stringValue(d, &value) != nil {
				return domain.ErrInvalidArgument
			}
			duration, err := strconv.ParseInt(*value, 10, 64)
			if err != nil || strconv.FormatInt(duration, 10) != *value {
				return domain.ErrInvalidArgument
			}
			v := time.Duration(duration)
			input.ValidFor = &v
			return nil
		default:
			return domain.ErrInvalidArgument
		}
	})
	return target, input, err
}
func DecodeCertificateHashes(data []byte, id string) (string, domain.CertificateHashInput, error) {
	var input domain.CertificateHashInput
	present := false
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		if name != "certificate" {
			return domain.ErrInvalidArgument
		}
		present = true
		var value *string
		if stringValue(d, &value) != nil {
			return domain.ErrInvalidArgument
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(*value)
		if err != nil || base64.StdEncoding.EncodeToString(raw) != *value {
			return domain.ErrInvalidArgument
		}
		input.Certificate = raw
		return nil
	})
	if err == nil && !present {
		err = domain.ErrInvalidArgument
	}
	return target, input, err
}
func DecodeECH(data []byte, id string) (string, domain.ECHInput, error) {
	var input domain.ECHInput
	target, err := object(data, id, func(name string, d *json.Decoder) error {
		switch name {
		case "server_name":
			return stringValue(d, &input.ServerName)
		case "server_keys":
			return stringValue(d, &input.ServerKeys)
		case "format":
			var value *string
			if stringValue(d, &value) != nil {
				return domain.ErrInvalidArgument
			}
			input.Format = domain.ECHFormat(*value)
			return nil
		default:
			return domain.ErrInvalidArgument
		}
	})
	return target, input, err
}
