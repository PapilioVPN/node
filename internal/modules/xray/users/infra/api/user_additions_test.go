package api_test

import (
	"context"
	"errors"
	"testing"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"

	"github.com/PapilioVPN/node/internal/modules/xray/users/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/users/infra/api"
)

type additionsAPI struct{ client native.HandlerServiceClient }

func (a additionsAPI) Begin(ctx context.Context) (context.Context, func(), error) {
	return ctx, func() {}, nil
}
func (a additionsAPI) HandlerClient() (native.HandlerServiceClient, error) { return a.client, nil }

// Controlled SDK completion establishes late-ack policy without transport races.
type lateAdditionClient struct {
	native.HandlerServiceClient
	cancel context.CancelFunc
	calls  int
	list   func() (*native.ListInboundsResponse, error)
}

func (c *lateAdditionClient) ListInbounds(context.Context, *native.ListInboundsRequest, ...grpc.CallOption) (*native.ListInboundsResponse, error) {
	if c.list != nil {
		return c.list()
	}
	return &native.ListInboundsResponse{Inbounds: []*core.InboundHandlerConfig{{Tag: "in", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config"}}}}, nil
}

func TestUserAdditionsRejectNilSDKPreflightFacts(t *testing.T) {
	// Protobuf transports normalize a nil repeated message into an empty message.
	// Exercise an actually nil SDK result directly rather than claiming a server
	// can transmit it unchanged through gRPC.
	for _, response := range []*native.ListInboundsResponse{nil, {Inbounds: []*core.InboundHandlerConfig{nil}}} {
		client := &lateAdditionClient{cancel: func() {}, list: func() (*native.ListInboundsResponse, error) { return response, nil }}
		adapter := &api.Adapter{API: additionsAPI{client: client}}
		input := domain.InboundUserAdditions{InboundTag: "in", Users: []domain.InboundUser{{Email: "user", Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-only-id"}}}}}
		report, err := adapter.AddInboundUsers(t.Context(), input)
		if !errors.Is(err, domain.ErrInvalidInboundResponse) || errors.Is(err, domain.ErrUserMutationUnknown) || client.calls != 0 || len(report.Items) != 1 || report.Items[0].State != domain.UserMutationNotAttempted || !report.Items[0].AcknowledgedAt.IsZero() {
			t.Fatal("nil SDK preflight reached a native mutation")
		}
	}
}
func (c *lateAdditionClient) AlterInbound(context.Context, *native.AlterInboundRequest, ...grpc.CallOption) (*native.AlterInboundResponse, error) {
	c.calls++
	c.cancel()
	return &native.AlterInboundResponse{}, nil
}

func TestUserAdditionsKeepLateAcknowledgementsAndDoNotDispatchAfterCancellation(t *testing.T) {
	for _, count := range []int{1, 2} {
		ctx, cancel := context.WithCancel(t.Context())
		client := &lateAdditionClient{cancel: cancel}
		adapter := &api.Adapter{API: additionsAPI{client: client}}
		input := domain.InboundUserAdditions{InboundTag: "in", Users: make([]domain.InboundUser, count)}
		for i := range input.Users {
			input.Users[i] = domain.InboundUser{Email: "repeated", Account: domain.UserAccount{Protocol: domain.VLESS, VLESS: &domain.VLESSAccount{ID: "test-only-id"}}}
		}
		report, err := adapter.AddInboundUsers(ctx, input)
		if client.calls != 1 || len(report.Items) != count || report.Items[0].State != domain.UserMutationAcknowledged || report.Items[0].AcknowledgedAt.IsZero() || report.Items[0].Failure != nil {
			t.Fatal("late cancellation erased acknowledged completion")
		}
		if count == 1 && err != nil {
			t.Fatal("completed collection became failed after its last acknowledgement")
		}
		if count == 2 && (!errors.Is(err, context.Canceled) || report.Items[1].State != domain.UserMutationNotApplied || !errors.Is(report.Items[1].Failure, context.Canceled) || errors.Is(err, domain.ErrUserMutationUnknown)) {
			t.Fatal("undispatched next addition became uncertain or lost cancellation")
		}
		cancel()
	}
}

func TestEmptyUserAdditionIsANoOpButDoesNotHideCallerCancellation(t *testing.T) {
	adapter := &api.Adapter{} // No native resource may be borrowed for an empty input.
	if report, err := adapter.AddInboundUsers(t.Context(), domain.InboundUserAdditions{}); err != nil || report.Items == nil || len(report.Items) != 0 {
		t.Fatal("empty addition acquired native work")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.AddInboundUsers(ctx, domain.InboundUserAdditions{}); !errors.Is(err, context.Canceled) {
		t.Fatal("empty addition hid caller cancellation")
	}
}
