package observations

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Service struct {
	current func(context.Context, string) (Execution, error)
	slots   chan struct{}
}

func New(current func(context.Context, string) (Execution, error)) *Service {
	return &Service{current: current, slots: make(chan struct{}, 8)}
}

func (s *Service) GetRuntimeStats(ctx context.Context, id string) (Runtime, error) {
	ex, err := s.current(ctx, id)
	if err != nil {
		return Runtime{}, err
	}
	return Runtime{Execution: ex.Number, ReadySinceUnixNano: ex.ReadyAt.UnixNano(), UptimeSeconds: uint64(time.Since(ex.ReadyAt) / time.Second)}, nil
}

func (s *Service) GetTraffic(ctx context.Context, id, user string) (Entry, error) {
	if !validUser(user) {
		return Entry{}, ErrInvalid
	}
	rows, err := s.traffic(ctx, id, false)
	if err != nil {
		return Entry{}, err
	}
	r, ok := rows[user]
	if !ok {
		return Entry{}, ErrNotFound
	}
	return Entry{UserID: user, Counter: r}, nil
}

func (s *Service) ListTraffic(ctx context.Context, id string) ([]Entry, error) {
	rows, err := s.traffic(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return entries(rows), nil
}

func (s *Service) ResetTraffic(ctx context.Context, id string) ([]Entry, error) {
	rows, err := s.traffic(ctx, id, true)
	if err != nil {
		return nil, err
	}
	return entries(rows), nil
}

func (s *Service) traffic(ctx context.Context, id string, clear bool) (map[string]Counter, error) {
	ex, call, release, err := s.admit(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := ex.Native.Traffic(call, clear)
	return rows, finish(ctx, call, ex, err, clear)
}

func (s *Service) ListOnlineUsers(ctx context.Context, id string) ([]OnlineUser, error) {
	ex, call, release, err := s.admit(ctx, id)
	if err != nil {
		return nil, err
	}
	defer release()
	rows, err := ex.Native.Online(call)
	if err := finish(ctx, call, ex, err, false); err != nil {
		return nil, err
	}
	res := make([]OnlineUser, 0, len(rows))
	for id, n := range rows {
		res = append(res, OnlineUser{UserID: id, Devices: n})
	}
	slices.SortFunc(res, func(a, b OnlineUser) int { return strings.Compare(a.UserID, b.UserID) })
	return res, nil
}

func (s *Service) DisconnectUsers(ctx context.Context, id string, ids []string) error {
	if len(ids) == 0 || len(ids) > MaxKickIDs {
		return ErrInvalid
	}
	seen := make(map[string]struct{}, len(ids))
	for _, name := range ids {
		if _, ok := seen[name]; ok || !validUser(name) {
			return ErrInvalid
		}
		seen[name] = struct{}{}
	}
	ex, call, release, err := s.admit(ctx, id)
	if err != nil {
		return err
	}
	defer release()
	return finish(ctx, call, ex, ex.Native.Kick(call, ids), true)
}

func validUser(s string) bool {
	return s != "" && len(s) <= MaxUserIDBytes && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

func entries(rows map[string]Counter) []Entry {
	res := make([]Entry, 0, len(rows))
	for id, c := range rows {
		res = append(res, Entry{UserID: id, Counter: c})
	}
	slices.SortFunc(res, func(a, b Entry) int { return strings.Compare(a.UserID, b.UserID) })
	return res
}

func (s *Service) admit(ctx context.Context, id string) (Execution, context.Context, func(), error) {
	ex, err := s.current(ctx, id)
	if err != nil {
		return Execution{}, nil, nil, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return Execution{}, nil, nil, ErrCapacity
	}
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	stop := context.AfterFunc(ex.Lifetime, cancel)
	if ex.Lifetime.Err() != nil {
		cancel()
	}
	release := func() { stop(); cancel(); <-s.slots }
	if call.Err() != nil {
		release()
		if ctx.Err() != nil {
			return Execution{}, nil, nil, ctx.Err()
		}
		return Execution{}, nil, nil, ErrInactive
	}
	return ex, call, release, nil
}

func finish(parent, call context.Context, ex Execution, err error, mutating bool) error {
	if mutating {
		if err != nil {
			return err
		}
		if call.Err() != nil || ex.Lifetime.Err() != nil {
			return ErrEffectUnknown
		}
		return nil
	}
	if parent.Err() != nil {
		return parent.Err()
	}
	if ex.Lifetime.Err() != nil {
		return ErrInactive
	}
	if call.Err() != nil {
		return call.Err()
	}
	return err
}
