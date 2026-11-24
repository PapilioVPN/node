// Package status owns reads of the configured WireGuard interface.
package status

import (
	"context"
	"errors"
)

var (
	ErrNotFound    = errors.New("wireguard interface not found")
	ErrBusy        = errors.New("wireguard interface reader is busy")
	ErrUnavailable = errors.New("wireguard interface status is unavailable")
)

type Interface struct {
	Name          string
	MTU           uint32
	Up            bool
	Index         int
	Alias         string
	AppliedSHA256 string
}

type Reader interface {
	Read(context.Context, string) (Interface, error)
}

type Service struct {
	name        string
	reader      Reader
	peerStats   PeerStatsReader
	appliedHash func(Interface) string
}

func New(name string, reader Reader, peerStats PeerStatsReader, appliedHash func(Interface) string) *Service {
	return &Service{name: name, reader: reader, peerStats: peerStats, appliedHash: appliedHash}
}

func (service *Service) GetStatus(ctx context.Context, name string) (Interface, error) {
	if name == "" || name != service.name {
		return Interface{}, ErrNotFound
	}
	result, err := service.reader.Read(ctx, name)
	if err == nil && service.appliedHash != nil {
		result.AppliedSHA256 = service.appliedHash(result)
	}
	return result, err
}
