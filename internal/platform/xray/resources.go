package xray

import (
	"errors"
	"fmt"
)

// InstanceConfig binds a configured identity to an optional native API endpoint.
type InstanceConfig struct {
	ID  string
	API *Config
}

// Resources owns the native API clients shared by an instance's capabilities.
// Construction is effect-free; clients remain lazy until their first operation.
type Resources struct {
	clients map[string]*Client
	order   []*Client
}

func NewResources(config []InstanceConfig) (*Resources, error) {
	r := &Resources{clients: make(map[string]*Client, len(config))}
	for _, instance := range config {
		if _, exists := r.clients[instance.ID]; exists || instance.ID == "" {
			return nil, errors.Join(errors.New("invalid Xray API instance binding"), r.Close())
		}
		r.clients[instance.ID] = nil
		if instance.API == nil {
			continue
		}
		client, err := NewClient(*instance.API)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("construct instance %q API: %w", instance.ID, err), r.Close())
		}
		r.clients[instance.ID] = client
		r.order = append(r.order, client)
	}
	return r, nil
}

// Client borrows an existing owner, retaining configured-but-disabled identities.
func (r *Resources) Client(id string) (*Client, bool) {
	client, exists := r.clients[id]
	return client, exists
}

func (r *Resources) Close() error {
	var result error
	for i := len(r.order) - 1; i >= 0; i-- {
		result = errors.Join(result, r.order[i].Close())
	}
	return result
}
