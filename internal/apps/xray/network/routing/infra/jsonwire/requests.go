// Package jsonwire owns strict routing JSON contracts shared by HTTP and NATS.
package jsonwire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func decode(data []byte, target any) error {
	if len(data) > 2<<20 {
		return domain.ErrCapacity
	}
	if !utf8.Valid(data) {
		return domain.ErrInvalidArgument
	}
	scanner := json.NewDecoder(bytes.NewReader(data))
	if err := value(scanner, 0); err != nil {
		return domain.ErrInvalidArgument
	}
	if _, err := scanner.Token(); !errors.Is(err, io.EOF) {
		return domain.ErrInvalidArgument
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return domain.ErrInvalidArgument
	}
	return nil
}
func value(d *json.Decoder, depth int) error {
	if depth > 64 {
		return domain.ErrInvalidArgument
	}
	token, err := d.Token()
	if err != nil || token == nil {
		return domain.ErrInvalidArgument
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return domain.ErrInvalidArgument
			}
			seen[name] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return domain.ErrInvalidArgument
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return domain.ErrInvalidArgument
		}
	default:
		return domain.ErrInvalidArgument
	}
	return nil
}
func DecodeID(data []byte) (string, error) {
	var r struct {
		InstanceID string `json:"instance_id"`
	}
	err := decode(data, &r)
	return r.InstanceID, err
}
func DecodeList(data []byte) (domain.PageRequest, error) {
	var r struct {
		InstanceID string `json:"instance_id"`
		PageSize   int    `json:"page_size"`
		PageToken  string `json:"page_token"`
	}
	err := decode(data, &r)
	return domain.PageRequest{InstanceID: r.InstanceID, PageSize: r.PageSize, PageToken: r.PageToken}, err
}
func DecodeTag(data []byte) (string, string, error) {
	var r struct {
		InstanceID string `json:"instance_id"`
		Tag        string `json:"tag"`
	}
	err := decode(data, &r)
	return r.InstanceID, r.Tag, err
}
func DecodeAdd(data []byte) (domain.AddRequest, error) {
	var r struct {
		InstanceID string          `json:"instance_id"`
		RuleJSON   json.RawMessage `json:"rule_json"`
		Mode       domain.Mode     `json:"mode"`
		Confirm    bool            `json:"confirm_destructive"`
	}
	err := decode(data, &r)
	return domain.AddRequest{InstanceID: r.InstanceID, RuleJSON: r.RuleJSON, Mode: r.Mode, ConfirmDestructive: r.Confirm}, err
}
func DecodeProbe(data []byte) (domain.ProbeRequest, error) {
	var r struct {
		InstanceID string   `json:"instance_id"`
		Context    *Context `json:"context"`
		Selectors  []string `json:"field_selectors"`
		Publish    bool     `json:"publish_result"`
	}
	err := decode(data, &r)
	if err != nil {
		return domain.ProbeRequest{}, err
	}
	if r.Context == nil {
		return domain.ProbeRequest{}, domain.ErrInvalidArgument
	}
	return domain.ProbeRequest{InstanceID: r.InstanceID, Context: r.Context.domain(), FieldSelectors: r.Selectors, PublishResult: r.Publish}, nil
}
func DecodeOverride(data []byte) (domain.OverrideRequest, error) {
	var r struct {
		InstanceID string  `json:"instance_id"`
		Tag        string  `json:"balancer_tag"`
		Target     *string `json:"target"`
	}
	err := decode(data, &r)
	return domain.OverrideRequest{InstanceID: r.InstanceID, BalancerTag: r.Tag, Target: r.Target}, err
}
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, domain.ErrInvalidArgument), errors.Is(err, configs.ErrInvalidArgument), errors.Is(err, configs.ErrRejected):
		return "invalid_argument"
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, configs.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrUnsupported), errors.Is(err, configs.ErrUnsupported):
		return "failed_precondition"
	case errors.Is(err, domain.ErrCapacity), errors.Is(err, configs.ErrCapacity), errors.Is(err, configs.ErrTooLarge):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrNotRunning):
		return "engine_unavailable"
	case errors.Is(err, domain.ErrUncertain):
		return "restart_required"
	case errors.Is(err, domain.ErrExecutionChanged):
		return "execution_changed"
	case errors.Is(err, domain.ErrObservationExpired):
		return "observation_expired"
	default:
		return "unavailable"
	}
}
func Failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{ErrorCode(err)})
	return data
}
func Deadline(ctx context.Context, timeout time.Duration, values []string) (context.Context, context.CancelFunc, error) {
	deadline := time.Now().Add(timeout)
	if values != nil {
		if len(values) != 1 {
			return nil, nil, domain.ErrInvalidArgument
		}
		v, err := time.Parse(time.RFC3339Nano, values[0])
		if err != nil {
			return nil, nil, domain.ErrInvalidArgument
		}
		if v.Before(deadline) {
			deadline = v
		}
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	return ctx, cancel, nil
}
