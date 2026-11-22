package api_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// connection is controlled SDK-call evidence, not a native engine.
type connection struct {
	invoke func(string, any, any, []grpc.CallOption) error
}

func (c connection) Invoke(_ context.Context, method string, in, out any, options ...grpc.CallOption) error {
	return c.invoke(method, in, out, options)
}
func (connection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}
func TestCollectionCallGrowthAndProjection(t *testing.T) {
	for _, count := range []int{0, 1, 8, 32, 50, 51, 1000} {
		for _, tagsOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/tags=%t", count, tagsOnly), func(t *testing.T) {
				calls := 0
				client := api.New(connection{func(method string, in, out any, options []grpc.CallOption) error {
					calls++
					if method != "/xray.app.proxyman.command.HandlerService/ListInbounds" || in.(*handlers.ListInboundsRequest).IsOnlyTags != tagsOnly {
						t.Fatalf("wrong native projection %s %v", method, in)
					}
					bounded := false
					for _, option := range options {
						if size, ok := option.(grpc.MaxRecvMsgSizeCallOption); ok && size.MaxRecvMsgSize == domain.MaxResponseBytes {
							bounded = true
						}
					}
					if !bounded {
						t.Fatal("unbounded native response")
					}
					response := out.(*handlers.ListInboundsResponse)
					for range count {
						config := &core.InboundHandlerConfig{Tag: ""}
						if !tagsOnly {
							config.ReceiverSettings = &serial.TypedMessage{Type: "opaque.receiver", Value: []byte{1, 2}}
							config.ProxySettings = &serial.TypedMessage{Type: "opaque.proxy", Value: []byte{3}}
							config.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x07})
						}
						response.Inbounds = append(response.Inbounds, config)
					}
					return nil
				}})
				if tagsOnly {
					tags, err := client.Tags(context.Background())
					if err != nil || len(tags) != count {
						t.Fatal(tags, err)
					}
				} else {
					records, err := client.Records(context.Background())
					if err != nil || len(records) != count {
						t.Fatal(records, err)
					}
					if count > 0 {
						var decoded core.InboundHandlerConfig
						if err := proto.Unmarshal(records[0].Configuration, &decoded); err != nil || len(decoded.ProtoReflect().GetUnknown()) == 0 || decoded.ProxySettings.Type != "opaque.proxy" {
							t.Fatal("native bytes lost", err)
						}
					}
				}
				if calls != 1 {
					t.Fatalf("N+1: count=%d calls=%d", count, calls)
				}
			})
		}
	}
}
func TestNativeMutationEffectsAndUnknownFields(t *testing.T) {
	for _, mode := range []string{"success", "terminal", "lost", "unsent", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := api.New(connection{func(_ string, in, out any, options []grpc.CallOption) error {
				calls++
				config := in.(*handlers.AddInboundRequest).Inbound
				if config.Tag != "a" || len(config.ProtoReflect().GetUnknown()) == 0 {
					t.Fatal("add changed native configuration")
				}
				if mode != "unsent" {
					for _, option := range options {
						if receipt, ok := option.(grpc.PeerCallOption); ok {
							receipt.PeerAddr.Addr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
						}
					}
				}
				switch mode {
				case "terminal":
					return status.Error(codes.Unknown, "native handler failed")
				case "lost", "unsent":
					return status.Error(codes.Unavailable, "lost")
				case "unsupported":
					return status.Error(codes.Unimplemented, "not installed")
				}
				return nil
			}})
			config := &core.InboundHandlerConfig{Tag: "a", ReceiverSettings: &serial.TypedMessage{Type: "receiver"}, ProxySettings: &serial.TypedMessage{Type: "proxy"}}
			config.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x07})
			content, _ := proto.Marshal(config)
			result, err := client.Add(context.Background(), content)
			expected := domain.Unknown
			if mode == "success" {
				expected = domain.Completed
			} else if mode == "unsent" || mode == "unsupported" {
				expected = domain.NotApplied
			}
			if result.Effect != expected || result.Acknowledged != (mode == "success") || result.RequiresInstanceRestart != (mode == "lost") || calls != 1 || (err == nil) != (mode == "success") {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
		})
	}
}
func TestMalformedAddDoesNotSubmit(t *testing.T) {
	client := api.New(connection{func(string, any, any, []grpc.CallOption) error { t.Fatal("malformed input submitted"); return nil }})
	for _, input := range [][]byte{{255}, {}, {10, 1, 'a'}} {
		result, err := client.Add(context.Background(), input)
		if !errors.Is(err, domain.ErrInvalidArgument) || result.Effect != domain.NotApplied {
			t.Fatal(result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.Remove(ctx, "a")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
