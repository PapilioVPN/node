package services

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

// OnlineReader offers separate native reads without over-fetching other users or
// traffic. Successful collections transfer their item ownership to the caller.
type OnlineReader interface {
	ReadUserOnlineStats(context.Context, domain.UserEmail) (domain.UserOnline, error)
	ReadUserOnlineIPs(context.Context, domain.UserEmail) (domain.UserOnlineIPs, error)
	ReadOnlineUsers(context.Context) (domain.OnlineUsers, error)
}

type OnlineService struct {
	readers map[instances.InstanceID]OnlineReader
}

func NewOnlineService(readers map[instances.InstanceID]OnlineReader) *OnlineService {
	return &OnlineService{readers: maps.Clone(readers)}
}

func (s *OnlineService) reader(ctx context.Context, id instances.InstanceID) (OnlineReader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return nil, instances.ErrNotFound
	}
	if reader == nil {
		return nil, domain.ErrAPIDisabled
	}
	return reader, nil
}

func (s *OnlineService) GetUserOnlineStats(ctx context.Context, id instances.InstanceID, email domain.UserEmail) (domain.InstanceUserOnline, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceUserOnline{}, err
	}
	online, err := reader.ReadUserOnlineStats(ctx, email)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceUserOnline{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceUserOnline{}, err
	}
	return domain.InstanceUserOnline{InstanceID: id, Email: email, Online: online}, nil
}

func (s *OnlineService) ListUserOnlineIPs(ctx context.Context, id instances.InstanceID, email domain.UserEmail) (domain.InstanceUserOnlineIPs, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceUserOnlineIPs{}, err
	}
	ips, err := reader.ReadUserOnlineIPs(ctx, email)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceUserOnlineIPs{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceUserOnlineIPs{}, err
	}
	slices.SortFunc(ips.Items, func(a, b domain.OnlineIP) int { return cmp.Compare(a.IP, b.IP) })
	if err := ctx.Err(); err != nil {
		return domain.InstanceUserOnlineIPs{}, err
	}
	return domain.InstanceUserOnlineIPs{InstanceID: id, Email: email, IPs: ips}, nil
}

func (s *OnlineService) ListOnlineUsers(ctx context.Context, id instances.InstanceID) (domain.InstanceOnlineUsers, error) {
	reader, err := s.reader(ctx, id)
	if err != nil {
		return domain.InstanceOnlineUsers{}, err
	}
	users, err := reader.ReadOnlineUsers(ctx)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceOnlineUsers{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceOnlineUsers{}, err
	}
	slices.Sort(users.Emails)
	if err := ctx.Err(); err != nil {
		return domain.InstanceOnlineUsers{}, err
	}
	return domain.InstanceOnlineUsers{InstanceID: id, Users: users}, nil
}
