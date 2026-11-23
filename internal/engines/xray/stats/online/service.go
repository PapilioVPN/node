// Package online reads the active Xray execution's per-account IP observations.
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
	ErrTooLarge    = errors.New("Xray online IP list exceeds the response limit")
	ErrUnavailable = errors.New("Xray online statistics unavailable")
)

type reader interface {
	Count(context.Context, string, string) (int64, error)
	IPs(context.Context, string, string) (map[string]int64, error)
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
	return read(ctx, id, name, &service.counts, 128, service.reader.Count)
}

// IPs returns each tracked source IP and its last-seen Unix timestamp in seconds.
func (service *Service) IPs(ctx context.Context, id, name string) (map[string]int64, error) {
	return read(ctx, id, name, &service.lists, 16, service.reader.IPs)
}

func read[T any](ctx context.Context, id, name string, active *atomic.Int32, limit int32, query func(context.Context, string, string) (T, error)) (T, error) {
	var zero T
	if id == "" || len(name) == 0 || len(name) > 4096 {
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
	return query(ctx, id, name)
}
