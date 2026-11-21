//go:build linux

package state

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type Spec struct{ ID, Directory string }
type Collection struct {
	ordered []*Store
	stores  map[string]*Store
}

func NewCollection(specs []Spec) (*Collection, error) {
	c := &Collection{stores: make(map[string]*Store, len(specs))}
	for _, spec := range specs {
		if c.stores[spec.ID] != nil {
			return nil, domain.ErrInvalidArgument
		}
		store, err := New(spec.Directory, spec.ID)
		if err != nil {
			return nil, err
		}
		c.stores[spec.ID] = store
		c.ordered = append(c.ordered, store)
	}
	return c, nil
}
func (c *Collection) Store(id string) *Store { return c.stores[id] }
func (c *Collection) Open(ctx context.Context) error {
	for _, store := range c.ordered {
		if err := store.Open(ctx); err != nil {
			// No incoming operation can exist during source-store startup. Close
			// already opened handles even when the startup context has expired.
			return errors.Join(err, c.Close(context.Background()))
		}
	}
	return nil
}
func (c *Collection) Close(ctx context.Context) error {
	var result error
	for i := len(c.ordered) - 1; i >= 0; i-- {
		result = errors.Join(result, c.ordered[i].Close(ctx))
	}
	return result
}
