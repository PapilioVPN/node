package api_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNativeAccountMutationIsOneRPCAndPreservesUnknownFields(t *testing.T) {
	for _, mode := range []string{"success", "terminal", "lost", "unsent", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := api.New(connection{func(method string, in, out any, options []grpc.CallOption) error {
				calls++
				request := in.(*handlers.AlterInboundRequest)
				if method != "/xray.app.proxyman.command.HandlerService/AlterInbound" || request.Tag != "members" || request.Operation.Type != "xray.app.proxyman.command.AddUserOperation" {
					t.Fatal(method, request)
				}
				var operation handlers.AddUserOperation
				if err := proto.Unmarshal(request.Operation.Value, &operation); err != nil || operation.User == nil || len(operation.User.ProtoReflect().GetUnknown()) == 0 || operation.User.Account.Type != "future.account" {
					t.Fatal(&operation, err)
				}
				if mode != "unsent" {
					for _, option := range options {
						if peer, ok := option.(grpc.PeerCallOption); ok {
							peer.PeerAddr.Addr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
						}
					}
				}
				switch mode {
				case "terminal":
					return status.Error(codes.Unknown, "native failure")
				case "lost", "unsent":
					return status.Error(codes.Unavailable, "lost reply")
				case "unsupported":
					return status.Error(codes.Unimplemented, "not installed")
				}
				return nil
			}})
			user := &protocol.User{Email: "MixedCase@example.test", Account: &serial.TypedMessage{Type: "future.account", Value: []byte{1, 2}}}
			user.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
			content, _ := proto.Marshal(user)
			prepared, err := c.Prepare(content)
			if err != nil || calls != 0 {
				t.Fatal("preparation submitted native work", err, calls)
			}
			clear(content) // The accepted native object owns its decoded fields.
			result, err := prepared.Add(context.Background(), "members")
			expected := domain.Unknown
			if mode == "success" {
				expected = domain.Completed
			} else if mode == "unsupported" || mode == "unsent" {
				expected = domain.NotApplied
			}
			if result.Effect != expected || result.Email != user.Email || result.Acknowledged != (mode == "success") || result.RequiresInstanceRestart != (mode == "lost") || calls != 1 || (err == nil) != (mode == "success") {
				t.Fatal(result, err, calls)
			}
		})
	}
}
func TestNativeRemovalRetainsExplicitEmptyEmail(t *testing.T) {
	calls := 0
	c := api.New(connection{func(_ string, in, out any, _ []grpc.CallOption) error {
		calls++
		request := in.(*handlers.AlterInboundRequest)
		var operation handlers.RemoveUserOperation
		if request.Tag != "members" || request.Operation.Type != "xray.app.proxyman.command.RemoveUserOperation" || proto.Unmarshal(request.Operation.Value, &operation) != nil || operation.Email != "" {
			t.Fatal(request)
		}
		return nil
	}})
	if result, err := c.Remove(context.Background(), "members", ""); err != nil || !result.Acknowledged || calls != 1 {
		t.Fatal(result, err, calls)
	}
}
func TestMalformedAccountNeverSubmits(t *testing.T) {
	c := api.New(connection{func(string, any, any, []grpc.CallOption) error { t.Fatal("malformed user submitted"); return nil }})
	for _, input := range [][]byte{{0xff}, {}, {10, 1, 'a'}} {
		result, err := c.Add(context.Background(), "members", input)
		if !errors.Is(err, domain.ErrInvalidArgument) || result.Effect != domain.NotApplied {
			t.Fatal(result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := c.Remove(ctx, "members", "a"); !errors.Is(err, context.Canceled) || result.Effect != domain.NotApplied {
		t.Fatal(result, err)
	}
}
