package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/grpc/status"
)

func (h Handler) GetConfiguration(ctx context.Context, request *configsv1.GetConfigurationRequest) (*configsv1.DesiredConfiguration, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.State.Read(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	sources := make([]*configsv1.ConfigurationSource, len(result.Sources))
	for i, s := range result.Sources {
		sources[i] = &configsv1.ConfigurationSource{Name: s.Name, Content: s.Content}
	}
	return &configsv1.DesiredConfiguration{InstanceId: request.InstanceId, Metadata: metadataResult(result.Metadata), Sources: sources}, nil
}
func (h Handler) GetConfigurationStatus(ctx context.Context, request *configsv1.GetConfigurationRequest) (*configsv1.ConfigurationStatus, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.State.Metadata(ctx, request.InstanceId)
	if err != nil {
		return nil, failure(err)
	}
	return &configsv1.ConfigurationStatus{InstanceId: request.InstanceId, Metadata: metadataResult(result)}, nil
}
func (h Handler) ReplaceConfiguration(ctx context.Context, request *configsv1.ReplaceConfigurationRequest) (*configsv1.ConfigurationChange, error) {
	if request == nil || request.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Sources) > domain.MaxSources {
		return nil, failure(domain.ErrTooLarge)
	}
	sources := make([]domain.Source, len(request.Sources))
	for i, s := range request.Sources {
		if s == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = domain.Source{Name: s.Name, Content: s.Content}
	}
	if h.State == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.State.Replace(ctx, request.InstanceId, *request.ExpectedRevision, sources)
	return changeResult(request.InstanceId, result, err)
}
func (h Handler) ApplyConfiguration(ctx context.Context, request *configsv1.ApplyConfigurationRequest) (*configsv1.ConfigurationChange, error) {
	if request == nil || request.ExpectedRevision == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if h.State == nil {
		return nil, failure(domain.ErrUnsupported)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.State.Apply(ctx, request.InstanceId, *request.ExpectedRevision)
	return changeResult(request.InstanceId, result, err)
}
func changeResult(id string, result domain.Change, err error) (*configsv1.ConfigurationChange, error) {
	response := configurationChange(id, result)
	if err == nil {
		return response, nil
	}
	withDetails, detailErr := status.Convert(failure(err)).WithDetails(response)
	if detailErr != nil {
		return nil, failure(err)
	}
	return nil, withDetails.Err()
}
