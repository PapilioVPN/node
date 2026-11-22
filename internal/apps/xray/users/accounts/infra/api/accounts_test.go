package api_test

import (
	"context"
	"errors"
	"fmt"
	"math"
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

type connection struct {
	invoke func(string, any, any, []grpc.CallOption) error
}

func (c connection) Invoke(_ context.Context, method string, in, out any, options ...grpc.CallOption) error {
	return c.invoke(method, in, out, options)
}
func (connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
func TestCountDoesNotFetchAccounts(t *testing.T) {
	calls := 0
	c := api.New(connection{func(method string, in, out any, _ []grpc.CallOption) error {
		calls++
		if method != "/xray.app.proxyman.command.HandlerService/GetInboundUsersCount" || in.(*handlers.GetInboundUserRequest).Tag != "members" {
			t.Fatal(method, in)
		}
		out.(*handlers.GetInboundUsersCountResponse).Count = 51
		return nil
	}})
	count, err := c.Count(context.Background(), "members")
	if err != nil || count != 51 || calls != 1 {
		t.Fatal(count, err, calls)
	}
}
func TestTargetedAbsenceDoesNotBecomeZeroAccount(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			calls := 0
			c := api.New(connection{func(method string, in, out any, options []grpc.CallOption) error {
				calls++
				limited := false
				for _, option := range options {
					if bound, ok := option.(grpc.MaxRecvMsgSizeCallOption); ok && bound.MaxRecvMsgSize == domain.MaxAccountBytes {
						limited = true
					}
				}
				if !limited {
					t.Fatal("exact reply is unbounded")
				}
				input := in.(*handlers.GetInboundUserRequest)
				if input.Email != "selected@example.test" || method != "/xray.app.proxyman.command.HandlerService/GetInboundUsers" || input.Tag != "members" {
					t.Fatal(input)
				}
				user := &protocol.User{}
				if !missing {
					user.Email = "Selected@example.test"
					user.Level = 7
					user.Account = &serial.TypedMessage{Type: "future.account", Value: []byte{1, 2}}
					user.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
				}
				out.(*handlers.GetInboundUserResponse).Users = []*protocol.User{user}
				return nil
			}})
			user, err := c.Get(context.Background(), "members", "selected@example.test")
			if err != nil || calls != 1 || (user == nil) != missing {
				t.Fatal(user, err, calls)
			}
			if user != nil {
				var raw protocol.User
				if proto.Unmarshal(user.Configuration, &raw) != nil || len(raw.ProtoReflect().GetUnknown()) == 0 || raw.Email != "Selected@example.test" {
					t.Fatal("native readback was normalized or truncated")
				}
			}
		})
	}
}
func TestMalformedNativeResponsesAndFailuresRemainErrors(t *testing.T) {
	for _, count := range []int64{-1, math.MaxInt64} {
		c := api.New(connection{func(_ string, _ any, out any, _ []grpc.CallOption) error {
			out.(*handlers.GetInboundUsersCountResponse).Count = count
			return nil
		}})
		got, err := c.Count(context.Background(), "members")
		if count < 0 {
			if !errors.Is(err, domain.ErrInvalidResponse) {
				t.Fatal(got, err)
			}
		} else if err != nil || got != count {
			t.Fatal(got, err)
		}
	}
	for _, users := range [][]*protocol.User{{{Email: "unexpected", Account: nil}}, {{}, {}}} {
		c := api.New(connection{func(_ string, _ any, out any, _ []grpc.CallOption) error {
			out.(*handlers.GetInboundUserResponse).Users = users
			return nil
		}})
		if _, err := c.Get(context.Background(), "members", "a"); !errors.Is(err, domain.ErrInvalidResponse) {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		code codes.Code
		want error
	}{{codes.Unimplemented, domain.ErrUnsupported}, {codes.ResourceExhausted, domain.ErrCapacity}, {codes.Canceled, context.Canceled}} {
		ctx, cancel := context.WithCancel(context.Background())
		cause := status.Error(test.code, "native failure")
		c := api.New(connection{func(string, any, any, []grpc.CallOption) error {
			if test.code == codes.Canceled {
				cancel()
			}
			return cause
		}})
		_, err := c.Count(ctx, "members")
		cancel()
		if !errors.Is(err, cause) || !errors.Is(err, test.want) {
			t.Fatal(test.code, err)
		}
	}
}

func TestPreparedNativeAccountPreservesCredentialsAndDoesNotSubmitBeforeAdmission(t *testing.T) {
	user := &protocol.User{Email: "Exact", Level: 7, Account: &serial.TypedMessage{Type: "opaque.Account", Value: []byte{1, 2, 3}}}
	user.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
	calls := 0
	client := api.New(connection{func(method string, input, _ any, _ []grpc.CallOption) error {
		calls++
		r := input.(*handlers.AlterInboundRequest)
		var operation handlers.AddUserOperation
		if method != "/xray.app.proxyman.command.HandlerService/AlterInbound" || r.Tag != "members" || proto.Unmarshal(r.Operation.Value, &operation) != nil || !proto.Equal(operation.User, user) {
			t.Fatal("prepared native account changed", method, r)
		}
		return nil
	}})
	prepared, err := client.PrepareNative(user)
	if err != nil || calls != 0 || prepared.AccountType() != user.Account.Type {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := prepared.Add(ctx, "members")
	if !errors.Is(err, context.Canceled) || calls != 0 || result.Effect != domain.NotApplied {
		t.Fatal(result, err, calls)
	}
	result, err = prepared.Add(context.Background(), "members")
	if err != nil || calls != 1 || !result.Acknowledged || result.Email != user.Email {
		t.Fatal(result, err, calls)
	}
}
