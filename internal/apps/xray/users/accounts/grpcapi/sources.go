package grpcapi

import (
	"context"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
)

func (h Handler) AddAccountFromSources(ctx context.Context, request *v1.AddAccountFromSourcesRequest) (*v1.AccountOperation, error) {
	if request == nil || request.Email == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	if len(request.Sources) > configs.MaxSources {
		return nil, failure(domain.ErrCapacity)
	}
	sources := make([]configs.Source, len(request.Sources))
	for i, source := range request.Sources {
		if source == nil {
			return nil, failure(domain.ErrInvalidArgument)
		}
		sources[i] = configs.Source{Name: source.Name, Content: source.Content}
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Sources.Add(ctx, request.InstanceId, request.InboundTag, *request.Email, sources)
	return mutationResult(result, err)
}
