// Package state reads routing observations from the active Xray execution.
package state

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid          = errors.New("invalid Xray routing request")
	ErrBalancerNotFound = errors.New("Xray balancer not found")
	ErrBusy             = errors.New("too many Xray routing reads")
	ErrTooLarge         = errors.New("Xray routing response exceeds the size limit")
	ErrUnavailable      = errors.New("Xray routing state unavailable")
)

type Rule struct {
	RuleTag     string
	OutboundTag string
}

type BalancerInfo struct {
	OverrideTarget *string
	// A nil slice means Xray did not report targets. An empty non-nil list
	// means Xray reported zero targets.
	ReportedTargets []string
}

type reader interface {
	ListRoutingRules(context.Context, string) ([]Rule, error)
	GetBalancerInfo(context.Context, string, string) (BalancerInfo, error)
}

type Service struct {
	reader    reader
	lists     atomic.Int32
	balancers atomic.Int32
}

func New(reader reader) *Service { return &Service{reader: reader} }

// ListRoutingRules returns one ordered native observation, including empty tags.
func (service *Service) ListRoutingRules(ctx context.Context, id string) ([]Rule, error) {
	if id == "" {
		return nil, ErrInvalid
	}
	if err := admit(ctx, &service.lists, 16); err != nil {
		return nil, err
	}
	defer service.lists.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return service.reader.ListRoutingRules(ctx, id)
}

// GetBalancerInfo preserves the native override and the strategy's report independently.
func (service *Service) GetBalancerInfo(ctx context.Context, id, tag string) (BalancerInfo, error) {
	if id == "" || tag == "" || len(tag) > 4096 || !utf8.ValidString(tag) {
		return BalancerInfo{}, ErrInvalid
	}
	if err := admit(ctx, &service.balancers, 64); err != nil {
		return BalancerInfo{}, err
	}
	defer service.balancers.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return service.reader.GetBalancerInfo(ctx, id, tag)
}

func admit(ctx context.Context, active *atomic.Int32, limit int32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for count := active.Load(); count < limit; count = active.Load() {
		if active.CompareAndSwap(count, count+1) {
			return nil
		}
	}
	return ErrBusy
}
