package api_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestNativeMutationEffectsAndUnknownFields(t *testing.T) {
	for _, mode := range []string{"success", "terminal", "lost", "unsent", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := api.New(connection{func(_ string, in, out any, options []grpc.CallOption) error {
				calls++
				config := in.(*handlers.AddOutboundRequest).Outbound
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
			config := &core.OutboundHandlerConfig{Tag: "a", SenderSettings: &serial.TypedMessage{Type: "receiver"}, ProxySettings: &serial.TypedMessage{Type: "proxy"}}
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

func TestAdditionAllowsNativeOmittedSenderAndRejectsUnselectableNamedTags(t *testing.T) {
	calls := 0
	client := api.New(connection{func(_ string, in, out any, _ []grpc.CallOption) error {
		calls++
		configuration := in.(*handlers.AddOutboundRequest).Outbound
		if configuration.SenderSettings != nil {
			t.Fatal("omitted sender fabricated")
		}
		return nil
	}})
	for _, tag := range []string{"", "valid", "nul\x00tag", strings.Repeat("a", 4097)} {
		content, _ := proto.Marshal(&core.OutboundHandlerConfig{Tag: tag, ProxySettings: &serial.TypedMessage{Type: "opaque.proxy"}})
		result, err := client.Add(context.Background(), content)
		if tag == "" || tag == "valid" {
			if err != nil || !result.Acknowledged {
				t.Fatal(result, err)
			}
		} else if !errors.Is(err, domain.ErrInvalidArgument) || result.Effect != domain.NotApplied {
			t.Fatal(result, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
