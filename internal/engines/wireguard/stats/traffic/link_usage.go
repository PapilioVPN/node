package traffic

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

var ErrLinkResetDisabled = errors.New("wireguard link usage reset requires managed mode")

type LinkTrafficSample struct {
	Counters
	Index int
	Alias string
}

func (service *Service) GetInterfaceUsage(ctx context.Context, name string) (Counters, error) {
	return service.readInterfaceUsage(ctx, name, false)
}

func (service *Service) ResetInterfaceUsage(ctx context.Context, name string) (Counters, error) {
	return service.readInterfaceUsage(ctx, name, true)
}

func (service *Service) readInterfaceUsage(ctx context.Context, name string, reset bool) (Counters, error) {
	if name == "" || name != service.name {
		return Counters{}, status.ErrNotFound
	}
	if reset && !service.managed {
		return Counters{}, ErrLinkResetDisabled
	}
	if !service.linkRead.TryLock() {
		return Counters{}, status.ErrBusy
	}
	defer service.linkRead.Unlock()

	service.mu.Lock()
	revision := service.linkRevision
	service.mu.Unlock()
	sample, err := service.links.ReadInterfaceTraffic(ctx, name)
	service.mu.Lock()
	defer service.mu.Unlock()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Counters{}, ctxErr
	}
	if revision != service.linkRevision {
		return Counters{}, status.ErrBusy
	}
	if err != nil {
		if errors.Is(err, status.ErrNotFound) {
			service.linkBase = LinkTrafficSample{}
		}
		return Counters{}, err
	}
	base := service.linkBase
	if base.Index == 0 || sample.Index != base.Index || sample.Alias != base.Alias ||
		sample.RXBytes < base.RXBytes || sample.TXBytes < base.TXBytes {
		service.linkBase = sample
		return Counters{}, nil
	}
	if reset {
		service.linkBase = sample
	}
	return Counters{RXBytes: sample.RXBytes - base.RXBytes, TXBytes: sample.TXBytes - base.TXBytes}, nil
}
