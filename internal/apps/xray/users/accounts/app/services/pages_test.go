package services_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type collections struct {
	users []domain.Metadata
	calls int
}

func (c *collections) Metadata(context.Context, string) (domain.NativeMetadata, error) {
	c.calls++
	return domain.NativeMetadata{Users: c.users, ReadCost: domain.ReadCost{NativeBytes: len(c.users) * 100}}, nil
}
func (c *collections) Records(context.Context, string) (domain.NativeRecords, error) {
	c.calls++
	r := domain.NativeRecords{ReadCost: domain.ReadCost{NativeBytes: len(c.users) * 100}}
	for _, m := range c.users {
		r.Users = append(r.Users, domain.Record{Email: m.Email, Level: m.Level, Configuration: []byte("credential")})
	}
	return r, nil
}
func (c *collections) GetMetadata(context.Context, string, string) (*domain.Metadata, error) {
	c.calls++
	return nil, nil
}
func TestStablePagesDoNotRepeatNativeReadAndBindSelectionViewAndExecution(t *testing.T) {
	for _, count := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			e := &execution{state: instances.Running, generation: 1}
			reader := &collections{users: make([]domain.Metadata, count)}
			for i := range reader.users {
				reader.users[i] = domain.Metadata{Email: fmt.Sprint(i % 3), AccountType: "native.Account"}
			}
			s, _ := newService([]services.Binding{{ID: "a", Client: client{}, Reader: reader}, {ID: "b", Client: client{}, Reader: reader}}, e)
			p, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{})
			if err != nil {
				t.Fatal(err)
			}
			total := len(p.Users)
			cursor := p.NextCursor
			for cursor != "" {
				next, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{Cursor: cursor})
				if err != nil || next.ObservedAt != p.ObservedAt || len(next.Users) > 50 {
					t.Fatal(next, err)
				}
				total += len(next.Users)
				cursor = next.NextCursor
			}
			if total != count || reader.calls != 1 {
				t.Fatal(total, reader.calls)
			}
			if count > 50 {
				if _, err := s.Export(context.Background(), "a", "tag", domain.PageRequest{Cursor: p.NextCursor}); !errors.Is(err, domain.ErrStale) {
					t.Fatal("cross-view token accepted", err)
				}
				if _, err := s.ListMetadata(context.Background(), "b", "tag", domain.PageRequest{Cursor: p.NextCursor}); !errors.Is(err, domain.ErrStale) {
					t.Fatal("cross-instance token accepted", err)
				}
				if _, err := s.ListMetadata(context.Background(), "a", "other", domain.PageRequest{Cursor: p.NextCursor}); !errors.Is(err, domain.ErrStale) {
					t.Fatal("cross-inbound token accepted", err)
				}
				e.replace()
				if _, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{Cursor: p.NextCursor}); !errors.Is(err, domain.ErrStale) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCompletedLocalMembershipChangeDoesNotRefreshAnExistingObservation(t *testing.T) {
	e := &execution{state: instances.Running, generation: 1}
	reader := &collections{users: make([]domain.Metadata, 51)}
	c := client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
		return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
	}}
	s, _ := newService([]services.Binding{{ID: "a", Client: c, Reader: reader}}, e)
	page, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(context.Background(), "a", "tag", []byte("new account")); err != nil {
		t.Fatal(err)
	}
	next, err := s.ListMetadata(context.Background(), "a", "tag", domain.PageRequest{Cursor: page.NextCursor})
	if err != nil || len(next.Users) != 1 || reader.calls != 1 || next.ObservedAt != page.ObservedAt {
		t.Fatal("membership write invalidated immutable observation", next, err, reader.calls)
	}
}

func TestEmptyMetadataLookupCannotBecomeNativeCollectionRequest(t *testing.T) {
	reader := &collections{}
	s, err := newService([]services.Binding{{ID: "a", Client: client{}, Reader: reader}}, &execution{generation: 1, state: instances.Running})
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"", "nul\x00"} {
		if _, err := s.GetMetadata(context.Background(), "a", "members", email); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(err)
		}
	}
	if reader.calls != 0 {
		t.Fatal("invalid exact lookup reached native I/O", reader.calls)
	}
}
