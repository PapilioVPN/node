package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestMembershipReplacementPreservesUnselectedNativeDataAndInput(t *testing.T) {
	input := compiled(t, user(t, "old"))
	var original core.Config
	if err := proto.Unmarshal(input, &original); err != nil {
		t.Fatal(err)
	}
	original.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 9})
	original.Inbound[0].ProxySettings.Value = append(original.Inbound[0].ProxySettings.Value, 0xa0, 0x06, 8)
	input, _ = proto.Marshal(&original)
	before := bytes.Clone(input)
	selected := user(t, "new")
	result, err := configuration.ReplaceUsers(context.Background(), input, []domain.InboundUserSet{{InboundTag: "selected", Accounts: [][]byte{selected}}})
	if err != nil {
		t.Fatal(err)
	}
	var candidate core.Config
	if err := proto.Unmarshal(result, &candidate); err != nil || !proto.Equal(candidate.Inbound[1], original.Inbound[1]) || !bytes.Equal(candidate.ProtoReflect().GetUnknown(), original.ProtoReflect().GetUnknown()) || !bytes.HasSuffix(candidate.Inbound[0].ProxySettings.Value, selected) || !bytes.Contains(candidate.Inbound[0].ProxySettings.Value, []byte{0xa0, 0x06, 8}) {
		t.Fatal("replacement lost selected account or unselected native bytes", err)
	}
	if !bytes.Equal(input, before) {
		t.Fatal("input mutated")
	}
	clear, err := configuration.ReplaceUsers(context.Background(), result, []domain.InboundUserSet{{InboundTag: "selected"}})
	if err != nil || proto.Unmarshal(clear, &candidate) != nil || !bytes.Equal(candidate.Inbound[0].ProxySettings.Value, []byte{0xa0, 0x06, 8}) {
		t.Fatal("empty selection did not clear only accounts", err)
	}
}

func TestMembershipNativeTypesAmbiguityAndBounds(t *testing.T) {
	input := compiled(t, user(t, "old"))
	var config core.Config
	proto.Unmarshal(input, &config)
	config.Inbound = append(config.Inbound, proto.Clone(config.Inbound[0]).(*core.InboundHandlerConfig))
	ambiguous, _ := proto.Marshal(&config)
	wrong, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.trojan.Account"}})
	for _, test := range []struct {
		content  []byte
		accounts [][]byte
		cause    error
	}{
		{ambiguous, nil, domain.ErrInvalidArgument},
		{input, [][]byte{wrong}, domain.ErrInvalidArgument},
		{bytes.Repeat([]byte{0}, configs.MaxCompiledBytes+1), nil, domain.ErrCapacity},
		{input, [][]byte{bytes.Repeat([]byte{0}, domain.MaxAccountBytes+1)}, domain.ErrCapacity},
		{input, make([][]byte, configuration.MaxPreparedUsers+1), domain.ErrCapacity},
	} {
		result, err := configuration.ReplaceUsers(context.Background(), test.content, []domain.InboundUserSet{{InboundTag: "selected", Accounts: test.accounts}})
		if !errors.Is(err, test.cause) || result != nil {
			t.Fatal(len(result), err, test.cause)
		}
	}
}

func TestEmptyShadowsocksAESConfigBecomesMutableWithoutLosingNetwork(t *testing.T) {
	content := protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), "2022-blake3-aes-128-gcm")
	content = protowire.AppendString(protowire.AppendTag(content, 2, protowire.BytesType), "fixture-key")
	content = protowire.AppendBytes(protowire.AppendTag(content, 5, protowire.BytesType), []byte{2, 3})
	input, _ := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "a", ProxySettings: &serial.TypedMessage{Type: "xray.proxy.shadowsocks_2022.ServerConfig", Value: content}}}})
	result, err := configuration.ReplaceUsers(context.Background(), input, []domain.InboundUserSet{{InboundTag: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	var candidate core.Config
	proto.Unmarshal(result, &candidate)
	settings := candidate.Inbound[0].ProxySettings
	if settings.Type != "xray.proxy.shadowsocks_2022.MultiUserServerConfig" || !bytes.HasSuffix(settings.Value, []byte{34, 2, 2, 3}) {
		t.Fatal(settings)
	}
	for _, payload := range [][]byte{append(bytes.Clone(content), 0xa0, 0x06, 1), protowire.AppendString(protowire.AppendTag(nil, 1, protowire.BytesType), "2022-blake3-chacha20-poly1305")} {
		candidate.Inbound[0].ProxySettings.Type = "xray.proxy.shadowsocks_2022.ServerConfig"
		candidate.Inbound[0].ProxySettings.Value = payload
		input, _ := proto.Marshal(&candidate)
		if result, err := configuration.ReplaceUsers(context.Background(), input, []domain.InboundUserSet{{InboundTag: "a"}}); !errors.Is(err, domain.ErrUnsupported) || result != nil {
			t.Fatal(result, err)
		}
	}
}

func FuzzCompiledMembershipReplacement(f *testing.F) {
	f.Add(compiledSeed())
	f.Add([]byte{})
	f.Add([]byte{255})
	f.Fuzz(func(t *testing.T, content []byte) {
		before := bytes.Clone(content)
		result, err := configuration.ReplaceUsers(context.Background(), content, []domain.InboundUserSet{{InboundTag: "selected"}})
		if !bytes.Equal(content, before) {
			t.Fatal("mutated input")
		}
		if err == nil {
			var candidate core.Config
			if len(result) > configs.MaxCompiledBytes || proto.Unmarshal(result, &candidate) != nil {
				t.Fatal("invalid compiled result")
			}
		} else if result != nil {
			t.Fatal("failure returned partial candidate")
		}
	})
}

func TestMembershipReplacementRejectsInvalidSelectionsWithoutOutput(t *testing.T) {
	for _, sets := range [][]domain.InboundUserSet{
		nil,
		{{InboundTag: "missing"}},
		{{InboundTag: "selected"}, {InboundTag: "selected"}},
		{{InboundTag: ""}},
		{{InboundTag: "selected", Accounts: [][]byte{{255}}}},
		{{InboundTag: "selected", Accounts: [][]byte{{}}}},
	} {
		result, err := configuration.ReplaceUsers(context.Background(), compiled(t, user(t, "old")), sets)
		if !errors.Is(err, domain.ErrInvalidArgument) || result != nil {
			t.Fatal(result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := configuration.ReplaceUsers(ctx, compiled(t), []domain.InboundUserSet{{InboundTag: "selected"}}); !errors.Is(err, context.Canceled) || result != nil {
		t.Fatal(result, err)
	}
}

func TestMembershipPreservesNativeAccountOrderingDuplicatesAndAnonymousInputs(t *testing.T) {
	accounts := [][]byte{user(t, ""), user(t, "same"), user(t, "same")}
	result, err := configuration.ReplaceUsers(context.Background(), compiled(t), []domain.InboundUserSet{{InboundTag: "selected", Accounts: accounts}})
	if err != nil {
		t.Fatal(err)
	}
	var candidate core.Config
	if err := proto.Unmarshal(result, &candidate); err != nil {
		t.Fatal(err)
	}
	var expected []byte
	for _, account := range accounts {
		expected = protowire.AppendBytes(protowire.AppendTag(expected, 1, protowire.BytesType), account)
	}
	if !bytes.Equal(candidate.Inbound[0].ProxySettings.Value, expected) {
		t.Fatal("native account order or duplicate/anonymous inputs were changed")
	}
}

func TestMembershipCollectionBoundsAndGrowth(t *testing.T) {
	for _, size := range []int{1, 50, 51, 1000, configuration.MaxPreparedUsers} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			raw := user(t, "account")
			accounts := make([][]byte, size)
			for i := range accounts {
				accounts[i] = raw
			}
			output, err := configuration.ReplaceUsers(context.Background(), compiled(t), []domain.InboundUserSet{{InboundTag: "selected", Accounts: accounts}})
			if err != nil {
				t.Fatal(err)
			}
			var candidate core.Config
			if err := proto.Unmarshal(output, &candidate); err != nil {
				t.Fatal(err)
			}
			field := protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), raw)
			if !bytes.Equal(candidate.Inbound[0].ProxySettings.Value, bytes.Repeat(field, size)) {
				t.Fatal("collection contents or cardinality changed")
			}
		})
	}
}

func TestMembershipRejectsUnsupportedOrMalformedSelectedContainers(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload []byte
		cause   error
	}{
		{"xray.proxy.wireguard.DeviceConfig", []byte{64, 1}, domain.ErrUnsupported},
		{"unknown.container", nil, domain.ErrUnsupported},
		{"xray.proxy.vless.inbound.Config", []byte{8, 1}, domain.ErrInvalidArgument},
		{"xray.proxy.vless.inbound.Config", []byte{255}, domain.ErrInvalidArgument},
	} {
		input, _ := proto.Marshal(&core.Config{Inbound: []*core.InboundHandlerConfig{{Tag: "selected", ProxySettings: &serial.TypedMessage{Type: test.name, Value: test.payload}}}})
		if result, err := configuration.ReplaceUsers(context.Background(), input, []domain.InboundUserSet{{InboundTag: "selected"}}); !errors.Is(err, test.cause) || result != nil {
			t.Fatal(test.name, result, err)
		}
	}
}
