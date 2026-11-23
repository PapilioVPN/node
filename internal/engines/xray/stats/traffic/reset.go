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
	if !validResetName(counter.ID, counter.Scope, counter.Name) ||
		(counter.Direction != Uplink && counter.Direction != Downlink) {
		return 0, ErrInvalid
	}
	return withLimit(ctx, &service.resets, maxConcurrentResets, ErrResetBusy, func(ctx context.Context) (int64, error) {
		return service.counters.Reset(ctx, counter)
	})
}

const maxConcurrentResets = 16

func validResetName(id string, scope Scope, name string) bool {
	return id != "" && scope.valid() && name != "" && len(name) <= maxNameBytes && utf8.ValidString(name)
}

type CounterPair struct {
	ID    string
	Scope Scope
	Name  string
}

type ResetEffect string

const (
	ResetAcknowledged ResetEffect = "acknowledged"
	ResetMissing      ResetEffect = "missing"
	ResetNotApplied   ResetEffect = "not_applied"
	ResetUnknown      ResetEffect = "unknown"
	ResetNotAttempted ResetEffect = "not_attempted"
)

type DirectionReset struct {
	Effect        ResetEffect
	PreviousBytes int64
}

type PairReset struct {
	Uplink, Downlink DirectionReset
}

// ResetPair returns the outcome of each directional reset on one execution.
func (service *Service) ResetPair(ctx context.Context, pair CounterPair) (PairReset, error) {
	if !service.resetEnabled {
		return PairReset{}, ErrResetDisabled
	}
	if !validResetName(pair.ID, pair.Scope, pair.Name) {
		return PairReset{}, ErrInvalid
	}
	return withLimit(ctx, &service.resets, maxConcurrentResets, ErrResetBusy, func(ctx context.Context) (PairReset, error) {
		return service.counters.ResetPair(ctx, pair)
	})
}
