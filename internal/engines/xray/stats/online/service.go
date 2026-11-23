// Package online reads the active Xray execution's online account and IP observations.
package online

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid Xray online statistics request")
	ErrNotFound    = errors.New("Xray online IP observation not found")
	ErrBusy        = errors.New("too many Xray online statistics requests")
	ErrTooLarge    = errors.New("Xray online statistics list exceeds the response limit")
	ErrUnavailable = errors.New("Xray online statistics unavailable")
)

type reader interface {
	Count(context.Context, string, string) (int64, error)
	IPs(context.Context, string, string) (map[string]int64, error)
	Users(context.Context, string) ([]string, error)
}

type Service struct {
	reader reader
	counts atomic.Int32
	lists  atomic.Int32
}

func New(reader reader) *Service {
	return &Service{reader: reader}
}

func (service *Service) Count(ctx context.Context, id, name string) (int64, error) {
	if !validName(name) {
		return 0, ErrInvalid
	}
	return read(ctx, id, &service.counts, 128, func(ctx context.Context) (int64, error) {
		return service.reader.Count(ctx, id, name)
	})
}

// IPs returns each tracked source IP and its last-seen Unix timestamp in seconds.
func (service *Service) IPs(ctx context.Context, id, name string) (map[string]int64, error) {
	if !validName(name) {
		return nil, ErrInvalid
	}
	return read(ctx, id, &service.lists, 16, func(ctx context.Context) (map[string]int64, error) {
		return service.reader.IPs(ctx, id, name)
	})
}

// Users returns the account names with at least one tracked source IP.
func (service *Service) Users(ctx context.Context, id string) ([]string, error) {
	return read(ctx, id, &service.lists, 16, func(ctx context.Context) ([]string, error) {
		return service.reader.Users(ctx, id)
	})
}

func validName(name string) bool {
	return len(name) > 0 && len(name) <= 4096
}

func read[T any](ctx context.Context, id string, active *atomic.Int32, limit int32, query func(context.Context) (T, error)) (T, error) {
	var zero T
	if id == "" {
		return zero, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	for count := active.Load(); ; count = active.Load() {
		if count >= limit {
			return zero, ErrBusy
		}
		if active.CompareAndSwap(count, count+1) {
			break
		}
	}
	defer active.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return query(ctx)
}
