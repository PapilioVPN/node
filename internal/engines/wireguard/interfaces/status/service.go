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
	Name string
	MTU  uint32
	Up   bool
}

type Reader interface {
	Read(context.Context, string) (Interface, error)
}

type Service struct {
	name   string
	reader Reader
}

func New(name string, reader Reader) *Service {
	return &Service{name: name, reader: reader}
}

func (service *Service) GetStatus(ctx context.Context, name string) (Interface, error) {
	if name == "" || name != service.name {
		return Interface{}, ErrNotFound
	}
	return service.reader.Read(ctx, name)
}
