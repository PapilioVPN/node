package grpcapi

import (
	"context"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h Handler) PrepareAccountConfiguration(ctx context.Context, request *v1.PrepareAccountConfigurationRequest) (*configsv1.CompiledConfiguration, error) {
	if request == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Inbounds) > domain.MaxConfigurationUsers || len(request.Sources) > configs.MaxSources {
		return nil, failure(domain.ErrCapacity)
	}
	sources := make([]configs.Source, len(request.Sources))
	for i, source := range request.Sources {
		if source == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = configs.Source{Name: source.Name, Content: source.Content}
	}
	sets := make([]domain.InboundUserSet, len(request.Inbounds))
	for i, set := range request.Inbounds {
		if set == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sets[i] = domain.InboundUserSet{InboundTag: set.InboundTag, Accounts: set.Accounts}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Configurations.Prepare(ctx, request.InstanceId, sources, sets)
	if err != nil {
		return nil, failure(err)
	}
	return &configsv1.CompiledConfiguration{InstanceId: result.InstanceID, Content: result.Content, Sha256: result.SHA256, PreparedAt: timestamppb.New(result.PreparedAt), DurationNs: int64(result.Duration)}, nil
}
