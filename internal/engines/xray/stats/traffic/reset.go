package traffic

import (
	"context"
	"errors"
	"unicode/utf8"
)

var (
	ErrResetDisabled = errors.New("Xray traffic reset is disabled")
	ErrResetBusy     = errors.New("too many concurrent Xray traffic resets")
	ErrResetUnknown  = errors.New("Xray traffic reset outcome is unknown")
)

type Direction string

const (
	Uplink   Direction = "uplink"
	Downlink Direction = "downlink"
)

type Counter struct {
	ID        string
	Scope     Scope
	Name      string
	Direction Direction
}

// Reset atomically reads and clears one counter. The returned bytes belong to
// the selected counter before the reset acknowledged by Xray.
func (service *Service) Reset(ctx context.Context, counter Counter) (int64, error) {
	if !service.resetEnabled {
		return 0, ErrResetDisabled
	}
	if counter.ID == "" || !counter.Scope.valid() || counter.Name == "" ||
		len(counter.Name) > maxNameBytes || !utf8.ValidString(counter.Name) ||
		(counter.Direction != Uplink && counter.Direction != Downlink) {
		return 0, ErrInvalid
	}
	return withLimit(ctx, &service.resets, 16, ErrResetBusy, func(ctx context.Context) (int64, error) {
		return service.counters.Reset(ctx, counter)
	})
}
