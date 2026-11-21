package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) CompileConfiguration(ctx context.Context, request *configsv1.CompileConfigurationRequest) (*configsv1.CompiledConfiguration, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Sources) > domain.MaxSources {
		return nil, failure(domain.ErrTooLarge)
	}
	sources := make([]domain.Source, len(request.Sources))
	for i, source := range request.Sources {
		if source == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = domain.Source{Name: source.Name, Content: source.Content}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Compile(ctx, request.InstanceId, sources)
	if err != nil {
		return nil, failure(err)
	}
	return &configsv1.CompiledConfiguration{InstanceId: result.InstanceID, Content: result.Content, Sha256: result.SHA256, PreparedAt: timestamppb.New(result.PreparedAt), DurationNs: int64(result.Duration)}, nil
}
