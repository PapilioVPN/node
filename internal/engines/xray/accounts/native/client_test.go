package native

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

type activeExecution struct {
	conn atomic.Pointer[grpc.ClientConn]
}

func (e *activeExecution) NativeConnection(string) (*grpc.ClientConn, error) {
	return e.conn.Load(), nil
}

func TestUnacknowledgedMutationFencesThatExecution(t *testing.T) {
	const secret = "secret-native-credential"
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var lists, altered atomic.Int32
	server := func() *grpc.ClientConn {
		listener := bufconn.Listen(64 << 10)
		srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
			method, _ := grpc.MethodFromServerStream(stream)
			switch method {
			case listInbounds:
				if err := stream.RecvMsg(&ListInboundsRequest{}); err != nil {
					return err
				}
				lists.Add(1)
				return stream.SendMsg(&ListInboundsResponse{Inbounds: []*Inbound{
					{Tag: "vless", ProxySettings: &TypedMessage{Type: vlessInboundType}},
				}})
			case AlterInboundMethod:
				if err := stream.RecvMsg(&AlterInboundRequest{}); err != nil {
					return err
				}
				if altered.Add(1) == 1 {
					return status.Error(codes.Unavailable, secret)
				}
				return stream.SendMsg(&emptypb.Empty{})
			case CountUsersMethod:
				if err := stream.RecvMsg(&GetInboundUserRequest{}); err != nil {
					return err
				}
				return stream.SendMsg(&GetInboundUsersCountResponse{Count: 1})
			default:
				t.Errorf("unexpected method %s", method)
				return status.Error(codes.Unimplemented, "unexpected method")
			}
		}))
		done := make(chan error, 1)
		go func() { done <- srv.Serve(listener) }()
		t.Cleanup(func() {
			srv.Stop()
			if err := <-done; err != nil && err != grpc.ErrServerStopped {
				t.Error(err)
			}
		})
		conn, err := grpc.NewClient("passthrough:///account-fence", grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return listener.DialContext(ctx)
			}))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		return conn
	}
	execution := new(activeExecution)
	execution.conn.Store(server())
	service := membership.New(New(execution))
	scope := membership.Scope{InstanceID: "primary", InboundTag: "vless"}
	request := membership.AddRequest{Scope: scope, Email: "fixture", UUID: "6897cf23-5697-4641-bccc-3bd7ae1ca65b"}
	err := service.Add(ctx, request)
	if !errors.Is(err, membership.ErrUnknownEffect) || strings.Contains(err.Error(), secret) {
		t.Fatal("native error failed outcome classification or redaction")
	}
	if err := service.Add(ctx, request); !errors.Is(err, membership.ErrFenced) {
		t.Fatalf("retry error = %v, want fenced", err)
	}
	if count, err := service.Count(ctx, scope); err != nil || count != 1 {
		t.Fatalf("live count after uncertain mutation = %d, %v", count, err)
	}
	if lists.Load() != 1 || altered.Load() != 1 {
		t.Fatalf("native calls after fencing: lists=%d alterations=%d", lists.Load(), altered.Load())
	}
	execution.conn.Store(server())
	if err := service.Add(ctx, request); err != nil {
		t.Fatalf("add in replacement execution: %v", err)
	}
	if lists.Load() != 2 || altered.Load() != 2 {
		t.Fatalf("native calls after replacement: lists=%d alterations=%d", lists.Load(), altered.Load())
	}
}

func TestInventoryIncludesConfiguredAnonymousIDs(t *testing.T) {
	base := &VlessAccount{Id: "303fcd60-a8d5-4552-8b71-9a76ad9bda21"}
	account, err := pack(vlessAccountType, base)
	if err != nil {
		t.Fatal(err)
	}
	config, err := pack(vlessInboundType, &VlessInbound{Users: []*User{{Account: account}}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := buildInventory(nil, []*Inbound{{Tag: "vless", ProxySettings: config}})
	if err != nil {
		t.Fatal(err)
	}
	entry := state.inbounds["vless"]
	collision, _ := membership.ParseUUID("303fcd60-a8d5-7fff-8b71-9a76ad9bda21")
	if entry.ambiguous || len(entry.names) != 0 || entry.insert("new-user", membership.Key{Identity: collision.Key()}) {
		t.Fatal("configured anonymous account did not reserve its effective VLESS ID")
	}
}
