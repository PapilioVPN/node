package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type connection struct {
	invoke func(string, any, any, []grpc.CallOption) error
}

func (c connection) Invoke(_ context.Context, method string, in, out any, options ...grpc.CallOption) error {
	return c.invoke(method, in, out, options)
}
func (connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected native stream")
}
func TestNativeCollectionUsesOneBoundedCall(t *testing.T) {
	for _, count := range []int{1, 8, 32} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			calls := 0
			client := api.New(connection{func(method string, in, out any, options []grpc.CallOption) error {
				calls++
				if method != "/xray.app.proxyman.command.HandlerService/ListOutbounds" {
					t.Fatal(method)
				}
				bounded := false
				for _, option := range options {
					if size, ok := option.(grpc.MaxRecvMsgSizeCallOption); ok && size.MaxRecvMsgSize == domain.MaxResponseBytes {
						bounded = true
					}
				}
				if !bounded {
					t.Fatal("native receive limit missing")
				}
				result := out.(*handlers.ListOutboundsResponse)
				for range count {
					config := &core.OutboundHandlerConfig{ProxySettings: &serial.TypedMessage{Type: "opaque", Value: []byte{1, 2}}, Expire: 17, Comment: "future server may retain this"}
					config.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
					result.Outbounds = append(result.Outbounds, config)
				}
				return nil
			}})
			result, err := client.Records(context.Background())
			if err != nil || len(result) != count || calls != 1 {
				t.Fatal(err, len(result), calls)
			}
			var configuration core.OutboundHandlerConfig
			if err := proto.Unmarshal(result[0].Configuration, &configuration); err != nil || configuration.ProxySettings.Type != "opaque" || configuration.Expire != 17 || len(configuration.ProtoReflect().GetUnknown()) == 0 {
				t.Fatal(&configuration, err)
			}
		})
	}
}

func TestPreparedHandlerDefersSubmissionAndPreservesNativeObject(t *testing.T) {
	calls := 0
	config := &core.OutboundHandlerConfig{Tag: "prepared", ProxySettings: &serial.TypedMessage{Type: "proxy"}}
	config.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
	client := api.New(connection{func(method string, input, _ any, _ []grpc.CallOption) error {
		calls++
		request := input.(*handlers.AddOutboundRequest)
		if method != "/xray.app.proxyman.command.HandlerService/AddOutbound" || request.Outbound != config || len(request.Outbound.ProtoReflect().GetUnknown()) == 0 {
			t.Fatal("prepared object changed before SDK submission", method, request)
		}
		return nil
	}})
	prepared, err := client.PrepareNative(config)
	if err != nil || calls != 0 {
		t.Fatal("preparation submitted native work", err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := prepared.Add(ctx); !errors.Is(err, context.Canceled) || result.Effect != domain.NotApplied || calls != 0 {
		t.Fatal("canceled prepared handler submitted", result, err, calls)
	}
	result, err := prepared.Add(context.Background())
	if err != nil || !result.Acknowledged || result.Tag != config.Tag || calls != 1 {
		t.Fatal(result, err, calls)
	}
}
