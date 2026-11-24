package peers

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type Stats struct {
	PublicKey     [32]byte
	RXBytes       uint64
	TXBytes       uint64
	LastHandshake time.Time
}

type StatsReader interface {
	ReadPeerStats(context.Context, string) ([]Stats, error)
}

type StatsService struct {
	name   string
	reader StatsReader
}

func NewStatsService(name string, reader StatsReader) *StatsService {
	return &StatsService{name: name, reader: reader}
}

func (service *StatsService) ListPeerStats(ctx context.Context, name string) ([]Stats, error) {
	if name == "" || name != service.name {
		return nil, status.ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return service.reader.ReadPeerStats(ctx, name)
}
