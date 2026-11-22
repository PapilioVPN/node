// Package jsonwire owns strict, bounded asset management JSON contracts.
package jsonwire

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
)

func fields(d *json.Decoder, visit func(string) error) error {
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return domain.ErrInvalidArgument
	}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] {
			return domain.ErrInvalidArgument
		}
		seen[name] = true
		if err = visit(name); err != nil {
			return err
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return domain.ErrInvalidArgument
	}
	return nil
}
func text(d *json.Decoder) (string, error) {
	token, err := d.Token()
	value, ok := token.(string)
	if err != nil || !ok {
		return "", domain.ErrInvalidArgument
	}
	return value, nil
}
func decode(data []byte, external string, visit func(string, *json.Decoder) error) (string, error) {
	if len(data) > domain.MaxRequest {
		return "", domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return "", domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	id := external
	err := fields(d, func(name string) error {
		if name == "instance_id" {
			if external != "" {
				return domain.ErrInvalidArgument
			}
			var err error
			id, err = text(d)
			return err
		}
		return visit(name, d)
	})
	if err != nil {
		return "", err
	}
	if _, err = d.Token(); !errors.Is(err, io.EOF) {
		return "", domain.ErrInvalidArgument
	}
	if id == "" {
		return "", domain.ErrInvalidArgument
	}
	return id, nil
}
func Inspect(data []byte, external string) (string, error) {
	return decode(data, external, func(string, *json.Decoder) error { return domain.ErrInvalidArgument })
}
func upload(d *json.Decoder, maxBytes int) (domain.File, error) {
	var file domain.File
	count := 0
	err := fields(d, func(name string) error {
		value, err := text(d)
		if err != nil {
			return err
		}
		switch name {
		case "sha256":
			file.SHA256 = value
		case "content":
			if len(value) > base64.StdEncoding.EncodedLen(maxBytes) {
				return domain.ErrCapacity
			}
			file.Content, err = base64.StdEncoding.Strict().DecodeString(value)
			if err != nil {
				return domain.ErrInvalidArgument
			}
		default:
			return domain.ErrInvalidArgument
		}
		count++
		return nil
	})
	if err == nil && count != 2 {
		err = domain.ErrInvalidArgument
	}
	return file, err
}
func Stage(data []byte, external string) (string, domain.Candidate, error) {
	var candidate domain.Candidate
	present := false
	id, err := decode(data, external, func(name string, d *json.Decoder) error {
		if name != "candidate" {
			return domain.ErrInvalidArgument
		}
		present = true
		count := 0
		err := fields(d, func(name string) error {
			var err error
			switch name {
			case "binary":
				candidate.Binary, err = upload(d, domain.MaxBinary)
			case "geoip":
				candidate.GeoIP, err = upload(d, domain.MaxGeodata)
			case "geosite":
				candidate.GeoSite, err = upload(d, domain.MaxGeodata)
			default:
				return domain.ErrInvalidArgument
			}
			count++
			return err
		})
		if err == nil && count != 3 {
			return domain.ErrInvalidArgument
		}
		return err
	})
	if err == nil && !present {
		err = domain.ErrInvalidArgument
	}
	return id, candidate, err
}
func Activation(data []byte, external string, requireRelease bool) (string, domain.Activation, error) {
	var q domain.Activation
	revision := false
	execution := false
	release := false
	id, err := decode(data, external, func(name string, d *json.Decoder) error {
		value, err := text(d)
		if err != nil {
			return err
		}
		switch name {
		case "expected_revision":
			q.ExpectedRevision, err = strconv.ParseUint(value, 10, 64)
			if err != nil || strconv.FormatUint(q.ExpectedRevision, 10) != value {
				return domain.ErrInvalidArgument
			}
			revision = true
		case "execution_id":
			q.ExecutionID = value
			execution = true
		case "release_id":
			if !requireRelease {
				return domain.ErrInvalidArgument
			}
			q.ReleaseID = value
			release = true
		default:
			return domain.ErrInvalidArgument
		}
		return nil
	})
	if err == nil && (!revision || !execution || (requireRelease && !release)) {
		err = domain.ErrInvalidArgument
	}
	return id, q, err
}
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, domain.ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrBusy):
		return "busy"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrConflict):
		return "conflict"
	case errors.Is(err, domain.ErrRecovery):
		return "recovery_required"
	case errors.Is(err, domain.ErrUnsupported):
		return "failed_precondition"
	case errors.Is(err, domain.ErrRejected):
		return "native_rejected"
	default:
		return "unavailable"
	}
}

type ActivationResult struct {
	domain.Receipt
	ErrorCode string `json:"error_code"`
}

func Encode(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"unavailable"}`)
	}
	return data
}
func Failure(err error) []byte {
	return Encode(struct {
		Error string `json:"error"`
	}{ErrorCode(err)})
}
