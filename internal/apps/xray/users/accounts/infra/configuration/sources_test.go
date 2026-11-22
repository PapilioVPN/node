package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type compiler func(context.Context, string, []configs.Source) (configs.Compilation, error)

func (c compiler) Compile(ctx context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
	return c(ctx, id, sources)
}
func compiled(t *testing.T, users ...[]byte) []byte {
	t.Helper()
	var content []byte
	for _, user := range users {
		content = protowire.AppendBytes(protowire.AppendTag(content, 1, protowire.BytesType), user)
	}
	data, err := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "selected", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config", Value: content}}, {Tag: "excluded", ProxySettings: &serial.TypedMessage{Type: "opaque", Value: []byte{255}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func user(t *testing.T, email string) []byte {
	t.Helper()
	record := &protocol.User{Email: email, Level: 7, Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte{1, 2, 3}}}
	record.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestSelectionCompilesOnceAndPreservesNativeUserBytes(t *testing.T) {
	selected := user(t, "Exact@example.test")
	input := compiled(t, user(t, "other@example.test"), selected)
	calls := 0
	preparer := configuration.Preparer{Factories: map[string]configuration.Factory{"a": api.New(nil)}, Compiler: compiler(func(_ context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
		calls++
		if id != "a" || len(sources) != 2 || sources[1].Name != "tail.json" {
			t.Fatal(id, sources)
		}
		return configs.Compilation{Content: input}, nil
	})}
	result, err := preparer.Prepare(context.Background(), "a", "selected", "Exact@example.test", []configs.Source{{Name: "base.json"}, {Name: "tail.json"}})
	if err != nil || calls != 1 || !bytes.Equal(result.Configuration, selected) {
		t.Fatal(result, err, calls)
	}
	for i := range input {
		input[i] = 0
	}
	if !bytes.Equal(result.Configuration, selected) {
		t.Fatal("result still borrows compiled input")
	}
}
func TestSelectionRejectsMissingAmbiguousOrMismatchedAccounts(t *testing.T) {
	valid := user(t, "a")
	wrong, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.trojan.Account"}})
	for _, content := range [][]byte{compiled(t, user(t, "other")), compiled(t, valid, valid), compiled(t, wrong), compiled(t, []byte{255})} {
		p := configuration.Preparer{Factories: map[string]configuration.Factory{"a": api.New(nil)}, Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{Content: content}, nil
		})}
		if result, err := p.Prepare(context.Background(), "a", "selected", "a", nil); !errors.Is(err, domain.ErrInvalidArgument) || result.Configuration != nil {
			t.Fatal(result, err)
		}
	}
	for _, cause := range []error{configs.ErrRejected, configs.ErrTooLarge, configs.ErrUnsupported, context.Canceled} {
		p := configuration.Preparer{Factories: map[string]configuration.Factory{"a": api.New(nil)}, Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{}, cause
		})}
		if result, err := p.Prepare(context.Background(), "a", "selected", "a", nil); !errors.Is(err, cause) || result.Configuration != nil {
			t.Fatal(result, err)
		}
	}
}
func FuzzCompiledAccountSelection(f *testing.F) {
	f.Add(compiledSeed())
	f.Add([]byte{})
	f.Add([]byte{10, 1, 255})
	f.Fuzz(func(t *testing.T, content []byte) {
		p := configuration.Preparer{Factories: map[string]configuration.Factory{"a": api.New(nil)}, Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{Content: content}, nil
		})}
		result, err := p.Prepare(context.Background(), "a", "selected", "a", nil)
		if err == nil {
			var user protocol.User
			if len(result.Configuration) > domain.MaxAccountBytes || proto.Unmarshal(result.Configuration, &user) != nil || user.Email != "a" || user.Account == nil {
				t.Fatal("invalid selected account")
			}
		}
	})
}

func compiledSeed() []byte {
	raw, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account"}})
	payload := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), raw)
	output, _ := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "selected", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.vless.inbound.Config", Value: payload}}}})
	return output
}
func TestSelectionBoundsAndNativeClientMode(t *testing.T) {
	large, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: bytes.Repeat([]byte{1}, domain.MaxAccountBytes)}})
	p := configuration.Preparer{Factories: map[string]configuration.Factory{"a": api.New(nil)}, Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{Content: compiled(t, large)}, nil
	})}
	if result, err := p.Prepare(context.Background(), "a", "selected", "a", nil); !errors.Is(err, domain.ErrCapacity) || result.Configuration != nil {
		t.Fatal(len(result.Configuration), err)
	}
	peer, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.wireguard.PeerConfig"}})
	value := protowire.AppendBytes(protowire.AppendTag(nil, 5, protowire.BytesType), peer)
	value = protowire.AppendVarint(protowire.AppendTag(value, 8, protowire.VarintType), 1)
	content, _ := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "selected", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.wireguard.DeviceConfig", Value: value}}}})
	p.Compiler = compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{Content: content}, nil
	})
	if result, err := p.Prepare(context.Background(), "a", "selected", "a", nil); !errors.Is(err, domain.ErrUnsupported) || result.Configuration != nil {
		t.Fatal(result, err)
	}
}
