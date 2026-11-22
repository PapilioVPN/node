package api_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestNativeLightProjectionAndExactSelectionHaveOneRead(t *testing.T) {
	for _, count := range []int{0, 1, 50, 51, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			calls := 0
			client := api.New(connection{invoke: func(method string, input, output any, _ []grpc.CallOption) error {
				calls++
				if input.(*handlers.ListInboundsRequest).IsOnlyTags {
					t.Fatal("native tag-only response cannot prove type compatibility")
				}
				records := make([]*core.InboundHandlerConfig, count)
				for i := range records {
					// Opaque account/configuration bytes never become output fields.
					value := protowire.AppendTag(nil, 99, protowire.BytesType)
					value = protowire.AppendString(value, string(make([]byte, 256)))
					records[i] = &core.InboundHandlerConfig{Tag: fmt.Sprint(i), ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config", Value: value}}
				}
				output.(*handlers.ListInboundsResponse).Inbounds = records
				return nil
			}})
			result, err := client.Summaries(context.Background())
			if err != nil || len(result) != count || calls != 1 {
				t.Fatal(len(result), calls, err)
			}
			observed, err := client.Compatibility(context.Background(), "0")
			if count == 0 {
				if !errors.Is(err, domain.ErrInboundNotFound) {
					t.Fatal(err)
				}
			} else if err != nil || !observed.LiveAccounts || observed.AccountType != "xray.proxy.vless.Account" {
				t.Fatal(observed, err)
			}
			if calls != 2 {
				t.Fatal("per-item reads", calls)
			}
		})
	}
}
