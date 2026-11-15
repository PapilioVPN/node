package app

import (
	"context"
	"slices"

	"github.com/PapilioVPN/node/internal/modules/system/domain"
)

type InfoService struct {
	nodeID       domain.NodeID
	version      string
	capabilities []domain.Capability
}

func NewInfoService(nodeID domain.NodeID, version string, additional ...domain.Capability) *InfoService {
	values := slices.Clone(additional)
	slices.Sort(values)
	values = slices.DeleteFunc(slices.Compact(values), func(value domain.Capability) bool { return value == domain.ReadNodeInfo })
	return &InfoService{nodeID: nodeID, version: version, capabilities: append([]domain.Capability{domain.ReadNodeInfo}, values...)}
}

func (s *InfoService) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.NodeInfo{}, err
	}
	return domain.NodeInfo{
		NodeID:       s.nodeID,
		Version:      s.version,
		APIVersion:   "v1",
		Capabilities: slices.Clone(s.capabilities),
	}, nil
}
