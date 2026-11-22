package services_test

import (
	"context"
	"fmt"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type sourceCompiler func(context.Context, string, []configs.Source) (configs.Compilation, error)

func (c sourceCompiler) Compile(ctx context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
	return c(ctx, id, sources)
}

type sourceConnection struct {
	invoke func(string, any) error
}

func (c sourceConnection) Invoke(_ context.Context, method string, input, _ any, _ ...grpc.CallOption) error {
	return c.invoke(method, input)
}
func (sourceConnection) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}

func sourceBatchInput(t *testing.T, size int) ([]domain.SourceSelection, [][]byte, []byte) {
	t.Helper()
	items, raw := make([]domain.SourceSelection, size), make([][]byte, size)
	var payload []byte
	for i := range raw {
		email := fmt.Sprintf("Exact-%d", i)
		var err error
		raw[i], err = proto.Marshal(&protocol.User{Email: email, Level: uint32(i), Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte{1, 2, 3}}})
		if err != nil {
			t.Fatal(err)
		}
		payload = protowire.AppendBytes(protowire.AppendTag(payload, 1, protowire.BytesType), raw[i])
		items[size-1-i] = domain.SourceSelection{InboundTag: "members", Email: email}
	}
	compiled, err := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "members", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config", Value: payload}}}})
	if err != nil {
		t.Fatal(err)
	}
	return items, raw, compiled
}
