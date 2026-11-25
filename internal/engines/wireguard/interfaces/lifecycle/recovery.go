package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type Checkpoint struct {
	Link      Link        `json:"link"`
	SHA256    string      `json:"applied_config_sha256,omitempty"`
	MTU       uint32      `json:"mtu,omitempty"`
	Fixed     FixedConfig `json:"fixed"`
	StartedAt time.Time   `json:"started_at,omitzero"`
}

func (checkpoint Checkpoint) matches(link status.Interface) bool {
	return link.Up && checkpoint.MTU == link.MTU && checkpoint.Link.Name == link.Name &&
		checkpoint.Link.Index == link.Index && checkpoint.Link.Alias == link.Alias
}

type CheckpointStore interface {
	Load() (Checkpoint, error)
	Save(Checkpoint) error
	Clear() error
}

// Recover runs before the management listeners open. An ownership intent
// allows Stop to clean up a partially created link without attesting a config.
func (service *Service) Recover(ctx context.Context) error {
	if service.store == nil {
		return nil
	}
	checkpoint, err := service.store.Load()
	if err != nil || checkpoint.Link.Alias == "" {
		return err
	}
	if checkpoint.Link.Name != service.name {
		return errors.New("wireguard state belongs to another configured interface")
	}
	link, err := service.control.Inspect(ctx, checkpoint.Link)
	if errors.Is(err, status.ErrNotFound) || errors.Is(err, ErrConflict) {
		if service.routing != nil {
			if routeErr := service.routing.Remove(ctx, checkpoint.Link.Name, checkpoint.Link.Alias); routeErr != nil {
				return routeErr
			}
		}
		return service.clear()
	}
	if err != nil {
		return err
	}
	service.owned = checkpoint.Link
	service.owned.Index = link.Index
	if checkpoint.SHA256 != "" && checkpoint.matches(link) {
		if service.routing != nil {
			if routeErr := service.routing.Apply(ctx, checkpoint.Link.Name, checkpoint.Link.Alias); routeErr != nil {
				return routeErr
			}
		}
		service.applied.Store(&checkpoint)
		service.emit("recovered")
	}
	return nil
}

func (service *Service) save(checkpoint Checkpoint) error {
	if service.store == nil {
		return nil
	}
	return service.store.Save(checkpoint)
}

func (service *Service) clear() error {
	if service.store == nil {
		return nil
	}
	return service.store.Clear()
}
