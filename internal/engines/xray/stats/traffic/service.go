package traffic

import (
	"context"
	"strings"
	"time"
)

const (
	separator   = ">>>"
	uplink      = ">>>traffic>>>uplink"
	downlink    = ">>>traffic>>>downlink"
	singleLimit = 256 << 10
	listLimit   = 4 << 20
)

type source interface {
	Query(ctx context.Context, id, pattern string, maxBytes int) (Counters, error)
}

type Service struct {
	source  source
	singles chan struct{}
	lists   chan struct{}
}

func New(source source) *Service {
	return &Service{source: source, singles: make(chan struct{}, 128), lists: make(chan struct{}, 16)}
}

func (service *Service) Get(ctx context.Context, id string, scope Scope, name string) (Usage, error) {
	prefix, err := scope.prefix(id)
	if err != nil || name == "" || len(name) > 4096 {
		return Usage{}, ErrInvalid
	}
	base := prefix + name
	counters, err := service.query(ctx, id, base+">>>traffic>>>", singleLimit, service.singles)
	if err != nil {
		return Usage{}, err
	}
	usage := Usage{Name: name}
	for key, bytes := range counters.Each {
		switch key {
		case base + uplink:
			usage.UplinkBytes, usage.HasUplink = bytes, true
		case base + downlink:
			usage.DownlinkBytes, usage.HasDownlink = bytes, true
		}
	}
	if !usage.HasUplink && !usage.HasDownlink {
		return Usage{}, ErrNotFound
	}
	return usage, nil
}

func (service *Service) List(ctx context.Context, id string, scope Scope) ([]Usage, error) {
	prefix, err := scope.prefix(id)
	if err != nil {
		return nil, err
	}
	counters, err := service.query(ctx, id, prefix, listLimit, service.lists)
	if err != nil {
		return nil, err
	}
	entries := make([]Usage, 0, counters.Count/2)
	positions := make(map[string]int, counters.Count/2)
	for key, bytes := range counters.Each {
		key, matches := strings.CutPrefix(key, prefix)
		if !matches {
			continue
		}
		name, isUplink := strings.CutSuffix(key, uplink)
		if !isUplink {
			name, matches = strings.CutSuffix(key, downlink)
			if !matches {
				continue
			}
		}
		if name == "" {
			continue
		}
		index, exists := positions[name]
		if !exists {
			index = len(entries)
			positions[name] = index
			entries = append(entries, Usage{Name: name})
		}
		entry := &entries[index]
		if isUplink {
			entry.UplinkBytes, entry.HasUplink = bytes, true
		} else {
			entry.DownlinkBytes, entry.HasDownlink = bytes, true
		}
	}
	return entries, nil
}

func (scope Scope) prefix(id string) (string, error) {
	if id == "" {
		return "", ErrInvalid
	}
	switch scope {
	case User:
		return "user" + separator, nil
	case Inbound:
		return "inbound" + separator, nil
	case Outbound:
		return "outbound" + separator, nil
	default:
		return "", ErrInvalid
	}
}

func (service *Service) query(ctx context.Context, id, pattern string, maxBytes int, slots chan struct{}) (Counters, error) {
	if err := ctx.Err(); err != nil {
		return Counters{}, err
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return Counters{}, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return service.source.Query(ctx, id, pattern, maxBytes)
}
