// Package status owns reads of the configured WireGuard interface.
package status

import (
	"context"
	"errors"
	"net/netip"
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
	Configuration *Configuration
}

type Configuration struct {
	PublicKey  [32]byte
	ListenPort uint16
	Addresses  []netip.Prefix
}

type Reader interface {
	Read(context.Context, string) (Interface, error)
}

type Service struct {
	name              string
	reader            Reader
	appliedHash       func(Interface) string
	readConfiguration func(context.Context, Interface) (Configuration, error)
}

func New(name string, reader Reader, appliedHash func(Interface) string, readConfiguration func(context.Context, Interface) (Configuration, error)) *Service {
	return &Service{name: name, reader: reader, appliedHash: appliedHash, readConfiguration: readConfiguration}
}

func (service *Service) GetStatus(ctx context.Context, name string, includeConfiguration bool) (Interface, error) {
	if name == "" || name != service.name {
		return Interface{}, ErrNotFound
	}
	result, err := service.reader.Read(ctx, name)
	if err != nil {
		return Interface{}, err
	}
	if includeConfiguration {
		configuration, err := service.readConfiguration(ctx, result)
		if err != nil {
			return Interface{}, err
		}
		result.Configuration = &configuration
	}
	if service.appliedHash != nil {
		result.AppliedSHA256 = service.appliedHash(result)
	}
	return result, nil
}
