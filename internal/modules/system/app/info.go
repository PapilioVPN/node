package app

import (
	"context"

	"github.com/PapilioVPN/node/internal/modules/system/domain"
)

type InfoService struct {
	nodeID  domain.NodeID
	version string
}

func NewInfoService(nodeID domain.NodeID, version string) *InfoService {
	return &InfoService{nodeID: nodeID, version: version}
}

func (s *InfoService) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.NodeInfo{}, err
	}
	return domain.NodeInfo{
		NodeID:       s.nodeID,
		Version:      s.version,
		APIVersion:   "v1",
		Capabilities: []domain.Capability{domain.ReadNodeInfo, domain.ReadInstanceStatus},
	}, nil
}
