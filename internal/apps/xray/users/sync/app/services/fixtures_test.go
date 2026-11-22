package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	codec "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/proto"
)

type controlled struct {
	mu                                                  sync.Mutex
	users                                               map[string][]byte
	generation, revision                                uint64
	reads, adds, removes, prepares, applies, recoveries int
	failAt                                              int
	failRemove                                          bool
	unknown                                             bool
	entered, unblock                                    chan struct{}
	fullResult                                          ports.ResultV1
}

func user(email, secret string) []byte {
	b, _ := proto.Marshal(&protocol.User{Email: email, Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte(secret)}})
	return b
}
func newControlled(t *testing.T) (*Service, *controlled) {
	t.Helper()
	c := &controlled{users: make(map[string][]byte), generation: 3, revision: 7, failAt: -1, fullResult: ports.ResultV1{Revision: 8, Generation: 4, Effect: accounts.Completed, Durable: true, Ready: true}}
	s, err := New([]string{"one"}, c, c, c, codec.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}
func (c *controlled) Snapshot(ctx context.Context, id string, tags []string) (domain.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return domain.Snapshot{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := domain.Snapshot{Revision: domain.Revision{Config: c.revision, Generation: c.generation}}
	for _, tag := range tags {
		c.reads++
		in := domain.Inbound{Tag: tag, AccountType: "xray.proxy.vless.Account", EmailMatching: "lowercase", Live: true}
		for _, raw := range c.users {
			m, err := (codec.Codec{}).Decode(raw)
			if err != nil {
				return domain.Snapshot{}, err
			}
			in.Users = append(in.Users, m)
		}
		s.Inbounds = append(s.Inbounds, in)
	}
	return s, nil
}
func (c *controlled) AddMany(ctx context.Context, id string, items []accounts.Addition, parallel int) (accounts.Batch, error) {
	if c.entered != nil {
		close(c.entered)
		select {
		case <-c.unblock:
		case <-ctx.Done():
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if parallel != 1 {
		panic("dependent effects must be ordered")
	}
	var r accounts.Batch
	var cause error
	for i, item := range items {
		u, _ := (codec.Codec{}).Decode(item.Configuration)
		result := accounts.ItemResult{Index: i, Operation: accounts.Operation{InboundTag: item.InboundTag, Email: u.Email, Effect: accounts.NotApplied}, Err: accounts.ErrNotAttempted}
		if cause == nil {
			result.Attempted = true
			c.adds++
			if c.adds == c.failAt {
				cause = errors.New("private secret engine failure")
				result.Err = cause
				if c.unknown {
					result.Operation.Effect, result.Operation.RequiresInstanceRestart = accounts.Unknown, true
					c.users[u.Email] = item.Configuration
				}
			} else {
				c.users[u.Email] = item.Configuration
				result.Err = nil
				result.Operation.Effect, result.Operation.Acknowledged = accounts.Completed, true
			}
		}
		r.Items = append(r.Items, result)
	}
	return r, cause
}
func (c *controlled) RemoveMany(_ context.Context, id string, items []accounts.Removal, parallel int) (accounts.Batch, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var r accounts.Batch
	var cause error
	for i, item := range items {
		result := accounts.ItemResult{Index: i, Operation: accounts.Operation{Effect: accounts.NotApplied}, Err: accounts.ErrNotAttempted}
		if cause == nil {
			c.removes++
			result.Attempted = true
			if c.failRemove {
				cause = errors.New("private remove failure")
				result.Err = cause
			} else {
				delete(c.users, item.Email)
				result.Err = nil
				result.Operation.Effect, result.Operation.Acknowledged = accounts.Completed, true
			}
		}
		r.Items = append(r.Items, result)
	}
	return r, cause
}
func (c *controlled) Revision(context.Context, string) (uint64, error) { return c.revision, nil }
func (c *controlled) PlanMembership(context.Context, ports.MembershipV1) (ports.ReviewV1, error) {
	c.prepares++
	return ports.ReviewV1{Token: "config-v1", Mode: "restart", Reason: "membership_changed"}, nil
}
func (c *controlled) ApplyMembership(context.Context, string, string) (ports.ResultV1, error) {
	c.applies++
	return c.fullResult, nil
}
func (c *controlled) MembershipStatus(context.Context, string, string) (ports.ResultV1, error) {
	return c.fullResult, nil
}
func (c *controlled) RecoverMembership(context.Context, string, string, uint64) (ports.ResultV1, error) {
	c.recoveries++
	c.fullResult = ports.ResultV1{Revision: 9, Generation: 5, Effect: accounts.Completed, Durable: true, Ready: true}
	return c.fullResult, nil
}
func syncRequest(t *testing.T, s *Service, mode domain.Mode, count int) domain.Request {
	t.Helper()
	revision, err := s.Revision(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	r := domain.Request{InstanceID: "one", OperationID: "operation", Expected: revision, Mode: mode, Selection: []string{"selected"}, Sets: []accounts.InboundUserSet{{InboundTag: "selected", Accounts: [][]byte{}}}}
	for i := 0; i < count; i++ {
		r.Sets[0].Accounts = append(r.Sets[0].Accounts, user(fmt.Sprintf("u-%04d", i), "secret"))
	}
	return r
}

var _ policies.Decoder = codec.Codec{}
