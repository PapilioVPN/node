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

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/peers"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"github.com/google/uuid"
)

var (
	ErrConflict    = errors.New("wireguard interface is in use")
	ErrInvalid     = errors.New("invalid wireguard configuration")
	ErrTooLarge    = errors.New("wireguard configuration exceeds 1 MiB")
	ErrUnconfirmed = errors.New("wireguard configuration outcome could not be confirmed")
)

const OperationTimeout = 2 * time.Second

type Link struct {
	Name  string `json:"name"`
	Alias string `json:"alias"`
	Index int    `json:"index"`
}

type Control interface {
	Create(context.Context, Link, Config) (status.Interface, error)
	Inspect(context.Context, Link) (status.Interface, error)
	UpdateMTU(context.Context, Link, uint32, uint32) (status.Interface, error)
	Remove(context.Context, Link) error
}

type PeerTrafficObserver interface {
	ForgetPeerTraffic([32]byte)
	ForgetLinkTraffic()
}

type PeerUsageCapture interface {
	WithPeerRemoval(context.Context, [][32]byte, func() error) error
	WithLinkRemoval(context.Context, func() error) error
}

type Service struct {
	name        string
	control     Control
	peers       PeerControl
	store       CheckpointStore
	traffic     PeerTrafficObserver
	usage       PeerUsageCapture
	publish     func(string)
	reconfigure func(context.Context, Link, FixedConfig, Config, LiveField) (status.Interface, error)
	mu          sync.Mutex
	owned       Link
	peerState   *peers.State // Protected by mu and bound to the owned execution.
	applied     atomic.Pointer[Checkpoint]
}

func (service *Service) WithConfiguration(reconfigure func(context.Context, Link, FixedConfig, Config, LiveField) (status.Interface, error)) *Service {
	service.reconfigure = reconfigure
	return service
}

func (service *Service) WithEvents(publish func(string)) *Service {
	service.publish = publish
	return service
}

func (service *Service) emit(kind string) {
	if service.publish != nil {
		service.publish(kind)
	}
}

func (service *Service) RuntimeSince(ctx context.Context, name string) (time.Time, error) {
	if name == "" || name != service.name {
		return time.Time{}, status.ErrNotFound
	}
	if !service.mu.TryLock() {
		return time.Time{}, status.ErrBusy
	}
	defer service.mu.Unlock()
	current := service.applied.Load()
	if current == nil {
		return time.Time{}, status.ErrUnavailable
	}
	observed, err := service.control.Inspect(ctx, current.Link)
	if err != nil {
		return time.Time{}, err
	}
	if !current.matches(observed) {
		return time.Time{}, status.ErrUnavailable
	}
	return current.StartedAt, nil
}

func (service *Service) CheckApplied(ctx context.Context, name string) error {
	if name == "" || name != service.name {
		return status.ErrNotFound
	}
	if service.applied.Load() == nil {
		return status.ErrUnavailable
	}
	return ctx.Err()
}

// WithPeerTraffic binds observation invalidation during module composition.
func (service *Service) WithPeerTraffic(observer PeerTrafficObserver) *Service {
	service.traffic = observer
	return service
}

// WithPeerUsage binds pre-removal accounting during module composition.
func (service *Service) WithPeerUsage(capture PeerUsageCapture) *Service {
	service.usage = capture
	return service
}

func New(name string, control Control, peerControl PeerControl, store CheckpointStore) *Service {
	return &Service{name: name, control: control, peers: peerControl, store: store}
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
		if current.SHA256 != digest {
			return service.applyLive(ctx, current, source, digest)
		}
		result, err := service.control.Inspect(ctx, current.Link)
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
	if err := service.save(Checkpoint{Link: link}); err != nil {
		return status.Interface{}, err
	}
	result, err := service.control.Create(ctx, link, config)
	if errors.Is(err, ErrConflict) {
		return status.Interface{}, errors.Join(err, service.clear())
	}
	link.Index = result.Index
	service.owned = link
	accepted := Checkpoint{Link: link, SHA256: digest, MTU: config.MTU, Fixed: config.fixed(), StartedAt: time.Now().UTC()}
	if err == nil {
		err = service.save(accepted)
	}
	if err != nil {
		service.emit("start_failed")
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		cleanupErr := service.remove(cleanupCtx)
		return status.Interface{}, errors.Join(err, cleanupErr)
	}
	service.applied.Store(&accepted)
	if len(config.InitialPeers) > 0 {
		service.peerState = peers.NewState(peers.Snapshot{PublicKey: config.PublicKey(), Peers: config.InitialPeers})
	}
	service.emit("started")
	result.AppliedSHA256 = digest
	return result, nil
}

func (service *Service) applyLive(ctx context.Context, current *Checkpoint, source []byte, digest string) (status.Interface, error) {
	config, err := parseConfig(source)
	if err != nil {
		return status.Interface{}, err
	}
	field := current.liveField(config)
	if field == LiveConflict {
		return status.Interface{}, ErrConflict
	}
	var result status.Interface
	if field == LiveMTU {
		result, err = service.control.UpdateMTU(ctx, current.Link, current.MTU, config.MTU)
	} else if service.reconfigure != nil {
		result, err = service.reconfigure(ctx, current.Link, current.Fixed, config, field)
	} else {
		return status.Interface{}, ErrConflict
	}
	if errors.Is(err, ErrUnconfirmed) {
		service.applied.Store(nil)
	}
	if err != nil {
		return status.Interface{}, err
	}
	accepted := Checkpoint{Link: current.Link, SHA256: digest, MTU: config.MTU, Fixed: config.fixed(), StartedAt: current.StartedAt}
	if err := service.save(accepted); err != nil {
		service.applied.Store(nil)
		return status.Interface{}, errors.Join(ErrUnconfirmed, err)
	}
	service.applied.Store(&accepted)
	if field == LivePrivateKey {
		service.peerState = nil
	}
	service.emit("configuration_applied")
	result.AppliedSHA256 = digest
	return result, nil
}

func (service *Service) AppliedHash(link status.Interface) string {
	current := service.applied.Load()
	if current != nil && current.matches(link) {
		return current.SHA256
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
	service.peerState = nil
	if service.store != nil || service.owned.Alias == "" {
		return nil
	}
	return service.remove(ctx)
}

func (service *Service) remove(ctx context.Context) error {
	service.peerState = nil
	remove := func() error { return service.control.Remove(ctx, service.owned) }
	var err error
	if service.usage != nil && service.applied.Load() != nil {
		err = service.usage.WithLinkRemoval(ctx, remove)
	} else {
		err = remove()
	}
	if service.traffic != nil {
		service.traffic.ForgetLinkTraffic()
	}
	if err == nil || errors.Is(err, ErrConflict) {
		service.applied.Store(nil)
		if clearErr := service.clear(); clearErr != nil {
			return errors.Join(err, clearErr)
		}
		service.owned = Link{}
	}
	if err == nil {
		service.emit("stopped")
	} else {
		service.emit("stop_failed")
	}
	return err
}
