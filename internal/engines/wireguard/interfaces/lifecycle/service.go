// Package lifecycle owns managed WireGuard interface executions.
package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/google/uuid"
)

var (
	ErrConflict = errors.New("wireguard interface is in use")
	ErrInvalid  = errors.New("invalid wireguard configuration")
	ErrTooLarge = errors.New("wireguard configuration exceeds 1 MiB")
)

const OperationTimeout = 2 * time.Second

type Link struct {
	Name  string
	Alias string
	Index int
}

type Control interface {
	Create(context.Context, Link, Config) (status.Interface, error)
	Inspect(context.Context, Link) (status.Interface, error)
	Remove(context.Context, Link) error
}

type execution struct {
	link   Link
	digest string
	mtu    uint32
}

type Service struct {
	name    string
	control Control
	mu      sync.Mutex
	owned   Link
	applied atomic.Pointer[execution]
}

func New(name string, control Control) *Service {
	return &Service{name: name, control: control}
}

func (service *Service) Start(ctx context.Context, name string, source []byte) (status.Interface, error) {
	if name != service.name {
		return status.Interface{}, status.ErrNotFound
	}
	if len(source) > MaxConfigBytes {
		return status.Interface{}, ErrTooLarge
	}
	if !service.mu.TryLock() {
		return status.Interface{}, status.ErrBusy
	}
	defer service.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return status.Interface{}, err
	}
	sum := sha256.Sum256(source)
	digest := hex.EncodeToString(sum[:])
	if current := service.applied.Load(); current != nil {
		if current.digest != digest {
			return status.Interface{}, ErrConflict
		}
		result, err := service.control.Inspect(ctx, current.link)
		if err != nil {
			return status.Interface{}, err
		}
		if service.AppliedHash(result) == "" {
			return status.Interface{}, status.ErrUnavailable
		}
		result.AppliedSHA256 = digest
		return result, nil
	}
	if service.owned.Alias != "" {
		return status.Interface{}, ErrConflict
	}
	config, err := parseConfig(source)
	if err != nil {
		return status.Interface{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return status.Interface{}, err
	}
	link := Link{Name: name, Alias: "papiliovpn-node:" + id.String()}
	result, err := service.control.Create(ctx, link, config)
	if errors.Is(err, ErrConflict) {
		return status.Interface{}, err
	}
	link.Index = result.Index
	service.owned = link
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		cleanupErr := service.remove(cleanupCtx)
		return status.Interface{}, errors.Join(err, cleanupErr)
	}
	service.applied.Store(&execution{link: link, digest: digest, mtu: config.MTU})
	result.AppliedSHA256 = digest
	return result, nil
}

func (service *Service) AppliedHash(link status.Interface) string {
	current := service.applied.Load()
	if current != nil && link.Up && current.mtu == link.MTU &&
		link.Name == current.link.Name && link.Index == current.link.Index && link.Alias == current.link.Alias {
		return current.digest
	}
	return ""
}

func (service *Service) Stop(ctx context.Context, name string) error {
	if name != service.name {
		return status.ErrNotFound
	}
	if !service.mu.TryLock() {
		return status.ErrBusy
	}
	defer service.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if service.owned.Alias == "" {
		if _, err := service.control.Inspect(ctx, Link{Name: name}); errors.Is(err, status.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		return ErrConflict
	}
	return service.remove(ctx)
}

func (service *Service) Close(ctx context.Context) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.owned.Alias == "" {
		return nil
	}
	return service.remove(ctx)
}

func (service *Service) remove(ctx context.Context) error {
	err := service.control.Remove(ctx, service.owned)
	if err == nil || errors.Is(err, ErrConflict) {
		service.owned = Link{}
		service.applied.Store(nil)
	}
	return err
}
