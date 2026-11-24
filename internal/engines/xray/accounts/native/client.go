package native

import (
	"context"
	"errors"
	"iter"
	"maps"
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
	mutation  *semaphore.Weighted
	accepted  atomic.Pointer[acceptedMembership]
	recovery  *recoveryStore
}

func New(instances activeInstances) *Client {
	return &Client{instances: instances, loading: semaphore.NewWeighted(1), mutation: semaphore.NewWeighted(1)}
}

func (c *Client) Add(ctx context.Context, scope membership.Scope, account membership.Account) error {
	return c.edit(ctx, scope, true, func(e *editor) error {
		prepared, err := e.Prepare(account.Email, membership.Credential{Protocol: membership.VLESS, UUID: account.ID.String()})
		if err != nil {
			return err
		}
		return e.Add(prepared)
	})
}

func (c *Client) Remove(ctx context.Context, scope membership.Scope, email string) error {
	return c.edit(ctx, scope, true, func(e *editor) error { return e.Remove(membership.NameKey(email)) })
}

func (c *Client) Edit(ctx context.Context, instanceID string, tags []string, apply func(membership.EditSession) error) error {
	return c.editInbounds(ctx, instanceID, tags, false, apply)
}

func (c *Client) edit(ctx context.Context, scope membership.Scope, vlessOnly bool, apply func(*editor) error) error {
	return c.editInbounds(ctx, scope.InstanceID, []string{scope.InboundTag}, vlessOnly,
		func(session membership.EditSession) error { return apply(session.Inbounds()[0].(*editor)) })
}

func (c *Client) editInbounds(ctx context.Context, instanceID string, tags []string, vlessOnly bool, apply func(membership.EditSession) error) error {
	if !c.mutation.TryAcquire(1) {
		return membership.ErrBusy
	}
	defer c.mutation.Release(1)

	state, err := c.inventory(ctx, instanceID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !c.current(instanceID, state.conn) {
		return membership.ErrUnavailable
	}
	session := &editSession{editors: make([]membership.Editor, 0, len(tags))}
	if !vlessOnly {
		session.staged = c.accepted.Load().clone()
	}
	for _, tag := range tags {
		e, err := c.openEditor(ctx, state, membership.Scope{InstanceID: instanceID, InboundTag: tag}, vlessOnly)
		if err != nil {
			return err
		}
		e.staged = session.staged
		session.editors = append(session.editors, e)
	}
	if err := apply(session); err != nil {
		return err
	}
	if session.staged != nil {
		return c.accept(session.staged)
	}
	return nil
}

func (c *Client) openEditor(ctx context.Context, state *inventory, scope membership.Scope, vlessOnly bool) (*editor, error) {
	entry, err := state.selectInbound(scope.InboundTag, vlessOnly)
	if err != nil {
		return nil, err
	}
	if entry.ambiguous {
		return nil, membership.ErrAmbiguous
	}
	if entry.uncertain {
		return nil, membership.ErrFenced
	}
	return &editor{client: c, ctx: ctx, scope: scope, state: state, entry: entry}, nil
}

type editor struct {
	client *Client
	ctx    context.Context
	scope  membership.Scope
	state  *inventory
	entry  *inboundAccounts
	staged *acceptedMembership
}

func (e *editor) Name(email string) (string, error) { return e.entry.name(email) }

func (e *editor) Named() iter.Seq2[string, membership.Key] { return maps.All(e.entry.names) }

func (e *editor) Capacity() int { return maxAccounts - e.state.size }

func (e *editor) Lookup(name string) (membership.Key, bool) {
	key, found := e.entry.names[name]
	return key, found
}

func (e *editor) HasID(id membership.IDKey) bool {
	_, found := e.entry.ids[id]
	return found
}

func (e *editor) Prepare(email string, c membership.Credential) (membership.PreparedAccount, error) {
	return e.entry.prepare(email, c)
}

func (e *editor) Add(account membership.PreparedAccount) error {
	if _, exists := e.entry.names[account.Name]; exists || e.HasID(account.Identity) {
		return membership.ErrExists
	}
	if e.state.size >= maxAccounts {
		return membership.ErrTooLarge
	}
	user := e.user(account)
	operation, err := pack(AddUserOperationType, &AddUserOperation{User: user})
	if err != nil {
		return err
	}
	if err := e.client.alter(e.ctx, e.scope, e.state.conn, e.entry, operation); err != nil {
		return err
	}
	e.entry.names[account.Name] = account.Key
	e.entry.records[account.Name] = user
	e.entry.ids[account.Identity] = struct{}{}
	e.state.size++
	return nil
}

func (e *editor) Remove(name string) error {
	key, exists := e.entry.names[name]
	if !exists {
		return membership.ErrAccountMissing
	}

	operation, err := pack(RemoveUserOperationType, &RemoveUserOperation{Email: e.entry.records[name].Email})
	if err != nil {
		return err
	}
	if err := e.client.alter(e.ctx, e.scope, e.state.conn, e.entry, operation); err != nil {
		return err
	}
	delete(e.entry.names, name)
	delete(e.entry.records, name)
	delete(e.entry.ids, key.Identity)
	e.state.size--
	return nil
}

func (c *Client) Count(ctx context.Context, scope membership.Scope) (int64, error) {
	state, err := c.inventory(ctx, scope.InstanceID)
	if err != nil {
		return 0, err
	}
	if _, err := state.selectInbound(scope.InboundTag, true); err != nil {
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
