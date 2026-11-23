package state

import (
	"context"
	"errors"
	"net/netip"
	"time"
	"unicode/utf8"
)

var (
	ErrNoRoute         = errors.New("Xray found no matching route")
	ErrMutationUnknown = errors.New("Xray routing mutation outcome is unknown")
	ErrMutationFenced  = errors.New("Xray routing mutations are suspended for this execution")
	ErrMutationBusy    = errors.New("another Xray routing mutation is in progress")
	ErrUnsupported     = errors.New("Xray router does not support this operation")
)

type RouteProbe struct {
	InboundTag   string
	Network      string
	TargetIP     netip.Addr
	TargetPort   uint32
	TargetDomain string
	Protocol     string
	User         string
	Attributes   map[string]string
}

type RouteSelection struct {
	OutboundTag       string
	OutboundGroupTags []string
}

func (s *Service) TestRoute(ctx context.Context, id string, probe RouteProbe) (RouteSelection, error) {
	if id == "" || !probe.valid() {
		return RouteSelection{}, ErrInvalid
	}
	if err := admit(ctx, &s.probes, 16); err != nil {
		return RouteSelection{}, err
	}
	defer s.probes.Add(-1)
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.controller.TestRoute(ctx, id, probe)
}

func (s *Service) OverrideBalancerTarget(ctx context.Context, id, tag string, target *string) error {
	if id == "" || tag == "" || !validTag(tag) || target == nil || !validTag(*target) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.controller.OverrideBalancerTarget(ctx, id, tag, *target)
}

func (p RouteProbe) valid() bool {
	switch p.Network {
	case "", "tcp", "udp":
	default:
		return false
	}
	if p.TargetIP.Zone() != "" || p.TargetPort > 65535 || len(p.Attributes) > 32 {
		return false
	}
	for _, value := range [...]string{p.InboundTag, p.TargetDomain, p.Protocol, p.User} {
		if !validTag(value) {
			return false
		}
	}
	size := 0
	for key, value := range p.Attributes {
		if !validTag(key) || !validTag(value) {
			return false
		}
		size += len(key) + len(value)
	}
	return size <= 16<<10
}

func validTag(value string) bool { return len(value) <= 4096 && utf8.ValidString(value) }
