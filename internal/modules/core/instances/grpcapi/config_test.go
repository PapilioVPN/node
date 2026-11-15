package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
)

type instanceConfigValidator func(context.Context, domain.InstanceID, domain.ConfigCandidate) (domain.ConfigValidation, error)

func (f instanceConfigValidator) ValidateInstanceConfig(ctx context.Context, id domain.InstanceID, candidate domain.ConfigCandidate) (domain.ConfigValidation, error) {
	return f(ctx, id, candidate)
}

func TestConfigValidationAuthorizationAndRequestShape(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated},
		{verifiedContext(t), false, codes.PermissionDenied},
	} {
		// Nil Configs detects any application call before authentication/grants.
		handler := grpcapi.Handler{AllowStatusRead: true, AllowControl: true, AllowConfigValidation: test.grant}
		if _, err := handler.ValidateInstanceConfig(test.ctx, nil); status.Code(err) != test.code {
			t.Fatalf("authorization order: %v; want %v", err, test.code)
		}
	}
	handler := grpcapi.Handler{AllowConfigValidation: true}
	for _, request := range []*instancesv1.ValidateInstanceConfigRequest{
		nil, {}, {InstanceId: " "}, {InstanceId: "edge"}, {InstanceId: "edge", Format: 999},
	} {
		if _, err := handler.ValidateInstanceConfig(verifiedContext(t), request); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid shape reached application: %v", err)
		}
	}
}

func TestConfigValidationPreservesSelectionFormatsAndRejection(t *testing.T) {
	ctx := verifiedContext(t)
	content := []byte("native private content")
	for wire, native := range map[instancesv1.ConfigFormat]domain.ConfigFormat{
		instancesv1.ConfigFormat_CONFIG_FORMAT_JSON: domain.JSON,
		instancesv1.ConfigFormat_CONFIG_FORMAT_YAML: domain.YAML,
		instancesv1.ConfigFormat_CONFIG_FORMAT_TOML: domain.TOML,
	} {
		for _, valid := range []bool{true, false} {
			handler := grpcapi.Handler{AllowConfigValidation: true, Configs: instanceConfigValidator(func(received context.Context, id domain.InstanceID, candidate domain.ConfigCandidate) (domain.ConfigValidation, error) {
				if received != ctx || id != " edge " || candidate.Format != native || &candidate.Content[0] != &content[0] {
					t.Fatal("selection/format/borrowed content changed")
				}
				result := domain.ConfigValidation{Valid: valid}
				if !valid {
					result.Failure = errors.New("private native rejection")
				}
				return result, nil
			})}
			response, err := handler.ValidateInstanceConfig(ctx, &instancesv1.ValidateInstanceConfigRequest{InstanceId: " edge ", Format: wire, Content: content})
			if err != nil || response == nil || response.Valid != valid || strings.Contains(response.String(), "private") {
				t.Fatalf("build outcome leaked or changed: %v, %v", response, err)
			}
		}
	}
}

func TestConfigValidationErrorMappingAndCancellation(t *testing.T) {
	for cause, expected := range map[error]codes.Code{
		domain.ErrNotFound:                    codes.NotFound,
		domain.ErrValidationDisabled:          codes.FailedPrecondition,
		domain.ErrConfigTooLarge:              codes.ResourceExhausted,
		domain.ErrValidationBusy:              codes.ResourceExhausted,
		domain.ErrInvalidInput:                codes.InvalidArgument,
		context.Canceled:                      codes.Canceled,
		context.DeadlineExceeded:              codes.DeadlineExceeded,
		errors.New("controlled native error"): codes.Internal,
	} {
		handler := grpcapi.Handler{AllowConfigValidation: true, Configs: instanceConfigValidator(func(context.Context, domain.InstanceID, domain.ConfigCandidate) (domain.ConfigValidation, error) {
			return domain.ConfigValidation{Valid: true}, fmt.Errorf("private path or config: %w", cause)
		})}
		response, err := handler.ValidateInstanceConfig(verifiedContext(t), &instancesv1.ValidateInstanceConfigRequest{InstanceId: "edge", Format: instancesv1.ConfigFormat_CONFIG_FORMAT_JSON})
		if response != nil || status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("error became success or leaked: %v, %v; want %v", response, err, expected)
		}
	}
	ctx, cancel := context.WithCancel(verifiedContext(t))
	defer cancel()
	handler := grpcapi.Handler{AllowConfigValidation: true, Configs: instanceConfigValidator(func(context.Context, domain.InstanceID, domain.ConfigCandidate) (domain.ConfigValidation, error) {
		cancel()
		return domain.ConfigValidation{Valid: true}, nil
	})}
	if response, err := handler.ValidateInstanceConfig(ctx, &instancesv1.ValidateInstanceConfigRequest{InstanceId: "edge", Format: instancesv1.ConfigFormat_CONFIG_FORMAT_JSON}); response != nil || status.Code(err) != codes.Canceled {
		t.Fatalf("cancellation after validation became success: %v, %v", response, err)
	}
}
