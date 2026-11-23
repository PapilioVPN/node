package native

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	HandlerService          = "/xray.app.proxyman.command.HandlerService/"
	AlterInboundMethod      = HandlerService + "AlterInbound"
	CountUsersMethod        = HandlerService + "GetInboundUsersCount"
	listInbounds            = HandlerService + "ListInbounds"
	AddUserOperationType    = "xray.app.proxyman.command.AddUserOperation"
	RemoveUserOperationType = "xray.app.proxyman.command.RemoveUserOperation"
)

type activeInstances interface {
	NativeConnection(string) (*grpc.ClientConn, error)
}

type Client struct {
	instances activeInstances
	loading   *semaphore.Weighted
	cached    atomic.Pointer[inventory]
	mutation  sync.Mutex
}

func New(instances activeInstances) *Client {
	return &Client{instances: instances, loading: semaphore.NewWeighted(1)}
}

func (c *Client) Add(ctx context.Context, scope membership.Scope, account membership.Account) error {
	return c.Edit(ctx, scope, func(edit membership.Editor) error { return edit.Add(account) })
}

func (c *Client) Remove(ctx context.Context, scope membership.Scope, email string) error {
	return c.Edit(ctx, scope, func(edit membership.Editor) error { return edit.Remove(email) })
}

func (c *Client) Edit(ctx context.Context, scope membership.Scope, apply func(membership.Editor) error) error {
	if !c.mutation.TryLock() {
		return membership.ErrBusy
	}
	defer c.mutation.Unlock()

	state, entry, err := c.mutable(ctx, scope)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.current(scope.InstanceID, state.conn) {
		return membership.ErrUnavailable
	}
	return apply(&editor{client: c, ctx: ctx, scope: scope, state: state, entry: entry})
}

type editor struct {
	client *Client
	ctx    context.Context
	scope  membership.Scope
	state  *inventory
	entry  *inboundAccounts
}

func (e *editor) Lookup(email string) (membership.IDKey, bool) {
	id, found := e.entry.names[membership.NameKey(email)]
	return id, found
}

func (e *editor) HasID(id membership.IDKey) bool {
	_, found := e.entry.ids[id]
	return found
}

func (e *editor) Add(account membership.Account) error {
	name, id := membership.NameKey(account.Email), account.ID.Key()
	if _, exists := e.entry.names[name]; exists || e.HasID(id) {
		return membership.ErrExists
	}
	if e.state.size >= maxAccounts {
		return membership.ErrTooLarge
	}

	vless, err := pack(vlessAccountType, &VlessAccount{Id: account.ID.String()})
	if err != nil {
		return err
	}
	operation, err := pack(AddUserOperationType, &AddUserOperation{
		User: &User{Email: account.Email, Account: vless},
	})
	if err != nil {
		return err
	}
	if err := e.client.alter(e.ctx, e.scope, e.state.conn, e.entry, operation); err != nil {
		return err
	}
	e.entry.names[name] = id
	e.entry.ids[id] = struct{}{}
	e.state.size++
	return nil
}

func (e *editor) Remove(email string) error {
	name := membership.NameKey(email)
	id, exists := e.entry.names[name]
	if !exists {
		return membership.ErrAccountMissing
	}

	operation, err := pack(RemoveUserOperationType, &RemoveUserOperation{Email: email})
	if err != nil {
		return err
	}
	if err := e.client.alter(e.ctx, e.scope, e.state.conn, e.entry, operation); err != nil {
		return err
	}
	delete(e.entry.names, name)
	delete(e.entry.ids, id)
	e.state.size--
	return nil
}

func (c *Client) Count(ctx context.Context, scope membership.Scope) (int64, error) {
	state, err := c.inventory(ctx, scope.InstanceID)
	if err != nil {
		return 0, err
	}
	if _, err := state.vless(scope.InboundTag); err != nil {
		return 0, err
	}
	var result GetInboundUsersCountResponse
	err = state.conn.Invoke(ctx, CountUsersMethod, &GetInboundUserRequest{Tag: scope.InboundTag}, &result,
		grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024))
	if err := c.readResult(ctx, scope.InstanceID, state.conn, err); err != nil {
		return 0, err
	}
	if result.Count < 0 {
		return 0, membership.ErrUnavailable
	}
	return result.Count, nil
}

func (c *Client) mutable(ctx context.Context, scope membership.Scope) (*inventory, *inboundAccounts, error) {
	state, err := c.inventory(ctx, scope.InstanceID)
	if err != nil {
		return nil, nil, err
	}
	entry, err := state.vless(scope.InboundTag)
	if err != nil {
		return nil, nil, err
	}
	if entry.ambiguous {
		return nil, nil, membership.ErrAmbiguous
	}
	if entry.uncertain {
		return nil, nil, membership.ErrFenced
	}
	return state, entry, nil
}

func (c *Client) alter(ctx context.Context, scope membership.Scope, conn *grpc.ClientConn, entry *inboundAccounts, operation *TypedMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.current(scope.InstanceID, conn) {
		return membership.ErrUnavailable
	}
	err := conn.Invoke(ctx, AlterInboundMethod, &AlterInboundRequest{Tag: scope.InboundTag, Operation: operation},
		&emptypb.Empty{}, grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024))
	if err != nil || !c.current(scope.InstanceID, conn) {
		entry.uncertain = true
		return nativeError{kind: membership.ErrUnknownEffect, cause: err}
	}
	return nil
}

func pack(name string, value proto.Message) (*TypedMessage, error) {
	wire, err := proto.Marshal(value)
	if err != nil {
		return nil, nativeError{kind: membership.ErrUnavailable, cause: err}
	}
	return &TypedMessage{Type: name, Value: wire}, nil
}

func (c *Client) connection(id string) (*grpc.ClientConn, error) {
	conn, err := c.instances.NativeConnection(id)
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		return nil, membership.ErrInstanceMissing
	case err != nil:
		// Drop a cached inventory after observing its execution is no longer ready.
		if cached := c.cached.Load(); cached != nil && !c.current(id, cached.conn) {
			c.cached.CompareAndSwap(cached, nil)
		}
		return nil, nativeError{kind: membership.ErrUnavailable, cause: err}
	default:
		return conn, nil
	}
}

func (c *Client) current(id string, previous *grpc.ClientConn) bool {
	conn, err := c.instances.NativeConnection(id)
	return err == nil && conn == previous
}

func (c *Client) readResult(ctx context.Context, id string, conn *grpc.ClientConn, err error) error {
	if cause := ctx.Err(); cause != nil {
		return cause
	}
	if !c.current(id, conn) {
		return membership.ErrUnavailable
	}
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.ResourceExhausted:
		return nativeError{kind: membership.ErrTooLarge, cause: err}
	default:
		return nativeError{kind: membership.ErrUnavailable, cause: err}
	}
}

// Keep native diagnostics available to error inspection without including them
// in text that could reach management clients or logs.
type nativeError struct{ kind, cause error }

func (err nativeError) Error() string       { return err.kind.Error() }
func (err nativeError) Is(other error) bool { return err.kind == other }
func (err nativeError) Unwrap() error       { return err.cause }
