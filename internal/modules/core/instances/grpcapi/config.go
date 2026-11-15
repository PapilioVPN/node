package grpcapi

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type InstanceConfigValidator interface {
	ValidateInstanceConfig(context.Context, domain.InstanceID, domain.ConfigCandidate) (domain.ConfigValidation, error)
}

func (h Handler) ValidateInstanceConfig(ctx context.Context, request *instancesv1.ValidateInstanceConfigRequest) (*instancesv1.ValidateInstanceConfigResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowConfigValidation {
		return nil, status.Error(codes.PermissionDenied, "instance configuration validation is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "instance_id is required")
	}
	var format domain.ConfigFormat
	switch request.GetFormat() {
	case instancesv1.ConfigFormat_CONFIG_FORMAT_JSON:
		format = domain.JSON
	case instancesv1.ConfigFormat_CONFIG_FORMAT_YAML:
		format = domain.YAML
	case instancesv1.ConfigFormat_CONFIG_FORMAT_TOML:
		format = domain.TOML
	default:
		return nil, status.Error(codes.InvalidArgument, "a supported configuration format is required")
	}
	result, err := h.Configs.ValidateInstanceConfig(ctx, domain.InstanceID(request.GetInstanceId()), domain.ConfigCandidate{Format: format, Content: request.GetContent()})
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return nil, mapped
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil, status.Error(codes.NotFound, "instance not found")
	case errors.Is(err, domain.ErrValidationDisabled):
		return nil, status.Error(codes.FailedPrecondition, "instance configuration validation is not enabled")
	case errors.Is(err, domain.ErrConfigTooLarge):
		return nil, status.Error(codes.ResourceExhausted, "candidate configuration exceeds the instance byte limit")
	case errors.Is(err, domain.ErrValidationBusy):
		return nil, status.Error(codes.ResourceExhausted, "instance configuration validation is busy")
	case errors.Is(err, domain.ErrInvalidInput):
		return nil, status.Error(codes.InvalidArgument, "configuration content and a supported format are required")
	case err != nil:
		return nil, status.Error(codes.Internal, "cannot validate instance configuration")
	}
	return &instancesv1.ValidateInstanceConfigResponse{Valid: result.Valid}, nil
}
