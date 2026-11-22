package services

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type cacheStatus struct{}

func (cacheStatus) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Running, Generation: 1}}, nil
}

type cacheClient struct{ calls int }

func (*cacheClient) Count(context.Context, string) (int64, error) { panic("count preflight") }
func (*cacheClient) Get(context.Context, string, string) (*domain.Record, error) {
	panic("per-account read")
}
func (*cacheClient) Add(context.Context, string, []byte) (domain.NativeResult, error) {
	panic("unexpected write")
}
func (*cacheClient) Remove(context.Context, string, string) (domain.NativeResult, error) {
	panic("unexpected write")
}
func (c *cacheClient) Metadata(context.Context, string) (domain.NativeMetadata, error) {
	c.calls++
	return domain.NativeMetadata{Users: make([]domain.Metadata, 51)}, nil
}
func (*cacheClient) Records(context.Context, string) (domain.NativeRecords, error) {
	panic("metadata fetched export")
}
func (*cacheClient) GetMetadata(context.Context, string, string) (*domain.Metadata, error) {
	panic("per-account read")
}
func TestObservationCapacityAndExpiryRejectWithoutAnotherNativeRead(t *testing.T) {
	c := &cacheClient{}
	s := &Service{entries: map[string]*entry{"a": {client: c, reader: c}}, status: cacheStatus{}, reads: make(chan struct{}, 32)}
	var first domain.MetadataPage
	for i := 0; i < domain.MaxObservations; i++ {
		page, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = page
		}
	}
	if _, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{}); !errors.Is(err, domain.ErrCapacity) || c.calls != domain.MaxObservations {
		t.Fatal(err, c.calls)
	}
	for _, p := range s.pages.snapshots {
		p.expires = time.Now().Add(-time.Second)
	}
	if _, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{Cursor: first.NextCursor}); !errors.Is(err, domain.ErrStale) || c.calls != domain.MaxObservations {
		t.Fatal(err, c.calls)
	}
	if _, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{}); err != nil {
		t.Fatal("expired retention was not reclaimed", err)
	}
}
