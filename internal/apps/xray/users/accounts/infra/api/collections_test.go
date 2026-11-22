package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func TestCollectionUsesOneBoundedNativeReadAndPreservesEveryUser(t *testing.T) {
	for _, count := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			calls := 0
			response := &handlers.GetInboundUserResponse{}
			for i := 0; i < count; i++ {
				user := &protocol.User{Email: fmt.Sprint(i % 3), Level: uint32(i), Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte("private-credential")}}
				user.ProtoReflect().SetUnknown([]byte{0xa0, 6, 1})
				response.Users = append(response.Users, user)
			}
			c := api.New(connection{func(method string, input, output any, options []grpc.CallOption) error {
				calls++
				if method != "/xray.app.proxyman.command.HandlerService/GetInboundUsers" || input.(*handlers.GetInboundUserRequest).Email != "" {
					t.Fatal(method, input)
				}
				bound := false
				for _, option := range options {
					if b, ok := option.(grpc.MaxRecvMsgSizeCallOption); ok && b.MaxRecvMsgSize == domain.MaxCollectionBytes {
						bound = true
					}
				}
				if !bound {
					t.Fatal("missing native frame bound")
				}
				*output.(*handlers.GetInboundUserResponse) = *response
				return nil
			}})
			summary, err := c.Metadata(context.Background(), "inbound")
			if err != nil || len(summary.Users) != count || calls != 1 || summary.NativeBytes != proto.Size(response) {
				t.Fatal(summary, err, calls)
			}
			exported, err := c.Records(context.Background(), "inbound")
			if err != nil || len(exported.Users) != count || calls != 2 {
				t.Fatal(err, calls)
			}
			for i, record := range exported.Users {
				var actual protocol.User
				if proto.Unmarshal(record.Configuration, &actual) != nil || !proto.Equal(&actual, response.Users[i]) {
					t.Fatal("native User bytes narrowed", i)
				}
			}
		})
	}
}
func TestMalformedCollectionIsNotReportedAsCapacityOrPartialSuccess(t *testing.T) {
	c := api.New(connection{func(_ string, _, out any, _ []grpc.CallOption) error {
		out.(*handlers.GetInboundUserResponse).Users = []*protocol.User{nil}
		return nil
	}})
	if result, err := c.Records(context.Background(), "inbound"); !errors.Is(err, domain.ErrInvalidResponse) || len(result.Users) != 0 {
		t.Fatal(result, err)
	}
}
