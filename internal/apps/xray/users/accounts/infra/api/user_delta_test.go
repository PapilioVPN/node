package api_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
)

// Controlled SDK clients exercise ordering/cancellation without transport timing.
type deltaClient struct {
	native.HandlerServiceClient
	typeName string
	members  []*protocol.User
	calls    []string
	mutate   func(int) error
	observe  func()
}

func (c *deltaClient) ListInbounds(context.Context, *native.ListInboundsRequest, ...grpc.CallOption) (*native.ListInboundsResponse, error) {
	c.calls = append(c.calls, "inbounds")
	kind := c.typeName
	if kind == "" {
		kind = "xray.proxy.vless.inbound.Config"
	}
	return &native.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{{Tag: "in", ProxySettings: &serial.TypedMessage{Type: kind}}}}, nil
}

func (c *deltaClient) GetInboundUsers(_ context.Context, r *native.GetInboundUserRequest, _ ...grpc.CallOption) (*native.GetInboundUserResponse, error) {
	c.calls = append(c.calls, "users")
	if r.Tag != "in" || r.Email != "" {
		panic("delta added a per-user lookup")
	}
	if c.observe != nil {
		c.observe()
	}
	return &native.GetInboundUserResponse{Users: c.members}, nil
}

func (c *deltaClient) AlterInbound(_ context.Context, r *native.AlterInboundRequest, _ ...grpc.CallOption) (*native.AlterInboundResponse, error) {
	operation, err := r.Operation.GetInstance()
	if err != nil {
		return nil, err
	}
	switch op := operation.(type) {
	case *native.RemoveUserOperation:
		c.calls = append(c.calls, "remove:"+op.Email)
	case *native.AddUserOperation:
		c.calls = append(c.calls, "add:"+op.User.Email)
	default:
		panic("unexpected delta operation")
	}
	if c.mutate != nil {
		if err := c.mutate(len(c.calls) - 2); err != nil {
			return nil, err
		}
	}
	return &native.AlterInboundResponse{}, nil
}

func deltaUser(email string) domain.InboundUser {
	return domain.InboundUser{Email: email, Level: 31, Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "8e781ad6-dae6-4ed4-9d7b-8c247f735869"}}}
}

func TestUserDeltaPlansOneObservationAndRetainsCompletedRemovalAfterAddFailure(t *testing.T) {
	cause := errors.New("private-credential failure")
	client := &deltaClient{members: []*protocol.User{{Email: "remove"}, {Email: "REPLACE"}, {Email: "untouched"}}, mutate: func(call int) error {
		if call == 3 {
			return cause
		}
		return nil
	}}
	adapter := &api.Adapter{API: additionsAPI{client: client}}
	report, err := adapter.ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: "in", RemoveEmails: []string{"remove", "absent"}, Users: []domain.InboundUser{deltaUser("replace"), deltaUser("new")}})
	if !errors.Is(err, cause) || !errors.Is(err, domain.ErrUserMutationUnknown) || report.ObservedAt.IsZero() || len(report.Steps) != 5 {
		t.Fatalf("delta lost partial progress: %v, steps=%d", err, len(report.Steps))
	}
	want := []domain.UserMutationState{domain.UserMutationAcknowledged, domain.UserMutationNotRequired, domain.UserMutationAcknowledged, domain.UserMutationUnknown, domain.UserMutationNotAttempted}
	for i, state := range want {
		if report.Steps[i].State != state || report.Steps[i].AcknowledgedAt.IsZero() != (state != domain.UserMutationAcknowledged) {
			t.Fatalf("wrong effect certainty at step %d", i)
		}
	}
	if len(client.calls) != 5 || client.calls[0] != "inbounds" || client.calls[1] != "users" || client.calls[2] != "remove:remove" || client.calls[3] != "remove:replace" || client.calls[4] != "add:replace" {
		t.Fatalf("delta repeated reads, changed ordering or continued after failure: %v", client.calls)
	}
}

func TestUserWritesCannotInterleaveACollectionWithOtherNativeMutations(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	client := &writeAdmissionClient{entered: entered, release: release, calls: &calls}
	adapter := &api.Adapter{API: additionsAPI{client: client}}
	finished := make(chan error, 1)
	go func() {
		_, err := adapter.AddInboundUsers(t.Context(), domain.InboundUserAdditions{InboundTag: "in", Users: []domain.InboundUser{deltaUser("first")}})
		finished <- err
	}()
	defer func() {
		close(release)
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	for _, write := range []func() error{
		func() error {
			_, err := adapter.RemoveInboundUser(t.Context(), domain.InboundUserSelection{InboundTag: "other", Email: "user"})
			return err
		},
		func() error {
			_, err := adapter.AddInboundUser(t.Context(), domain.InboundUserAddition{InboundTag: "other", User: deltaUser("user")})
			return err
		},
		func() error {
			_, err := adapter.AddInboundUsers(t.Context(), domain.InboundUserAdditions{InboundTag: "other", Users: []domain.InboundUser{deltaUser("user")}})
			return err
		},
		func() error {
			_, err := adapter.ApplyInboundUserDelta(t.Context(), domain.InboundUserDelta{InboundTag: "other", Users: []domain.InboundUser{deltaUser("user")}})
			return err
		},
	} {
		if err := write(); !errors.Is(err, domain.ErrAPIBusy) {
			t.Errorf("concurrent user write was not rejected before dispatch: %v", err)
		}
	}
	if observation, err := adapter.ReadInboundUser(t.Context(), domain.InboundUserSelection{InboundTag: "in", Email: "reader"}); err != nil || observation.User != nil || observation.ObservedAt.IsZero() {
		t.Fatal("user write admission blocked an independent read")
	}

	if calls.Load() != 1 {
		t.Error("a second writer interleaved native effects")
	}
}

type writeAdmissionClient struct {
	*deltaClient
	entered chan struct{}
	release chan struct{}
	calls   *atomic.Int32
}

func (c *writeAdmissionClient) ListInbounds(ctx context.Context, r *native.ListInboundsRequest, options ...grpc.CallOption) (*native.ListInboundsResponse, error) {
	return (&deltaClient{}).ListInbounds(ctx, r, options...)
}

func (c *writeAdmissionClient) AlterInbound(context.Context, *native.AlterInboundRequest, ...grpc.CallOption) (*native.AlterInboundResponse, error) {
	if c.calls.Add(1) == 1 {
		close(c.entered)
		<-c.release
	}
	return &native.AlterInboundResponse{}, nil
}

func (c *writeAdmissionClient) GetInboundUsers(context.Context, *native.GetInboundUserRequest, ...grpc.CallOption) (*native.GetInboundUserResponse, error) {
	return &native.GetInboundUserResponse{}, nil
}
