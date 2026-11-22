package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func TestSourceBatchPreservesDuplicatesAnonymousSelectionsAndOwnedBytes(t *testing.T) {
	anonymous, exact := user(t, ""), user(t, "Exact")
	input := compiled(t, anonymous, exact)
	p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{Content: input}, nil
	})}
	result, err := p.PrepareMany(context.Background(), "a", []domain.SourceSelection{{InboundTag: "selected", Email: "Exact"}, {InboundTag: "selected"}, {InboundTag: "selected", Email: "Exact"}, {InboundTag: "selected", Email: "exact"}}, nil)
	if err != nil || len(result) != 4 || !bytes.Equal(result[0].Configuration, exact) || !bytes.Equal(result[1].Configuration, anonymous) || !bytes.Equal(result[2].Configuration, exact) || !errors.Is(result[3].Err, domain.ErrInvalidArgument) {
		t.Fatal(result, err)
	}
	for i := range input {
		input[i] = 0
	}
	result[0].Configuration[0] ^= 0xff
	if !bytes.Equal(result[2].Configuration, exact) || !bytes.Equal(result[1].Configuration, anonymous) {
		t.Fatal("selected results share mutable storage")
	}
}

func TestSourceBatchRejectsAmbiguousInboundsAndAccountsAtTheirPositions(t *testing.T) {
	valid := user(t, "a")
	for _, duplicateInbound := range []bool{false, true} {
		input := compiled(t, valid, valid)
		if duplicateInbound {
			var c core.Config
			_ = proto.Unmarshal(compiled(t, valid), &c)
			c.Inbound = append(c.Inbound, c.Inbound[0], c.Inbound[0])
			input, _ = proto.Marshal(&c)
		}
		p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{Content: input}, nil
		})}
		result, err := p.PrepareMany(context.Background(), "a", []domain.SourceSelection{{InboundTag: "selected", Email: "a"}, {InboundTag: "absent", Email: "a"}}, nil)
		if err != nil || len(result) != 2 {
			t.Fatal(result, err)
		}
		for _, item := range result {
			if !errors.Is(item.Err, domain.ErrInvalidArgument) || item.Configuration != nil {
				t.Fatal(item)
			}
		}
	}
}

func TestSourceBatchEnforcesTotalSelectedBytesIncludingRepeatedSelections(t *testing.T) {
	raw, _ := proto.Marshal(&protocol.User{Email: "a", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: bytes.Repeat([]byte{1}, domain.MaxAccountBytes-100)}})
	p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{Content: compiled(t, raw)}, nil
	})}
	items := make([]domain.SourceSelection, 5)
	for i := range items {
		items[i] = domain.SourceSelection{InboundTag: "selected", Email: "a"}
	}
	if result, err := p.PrepareMany(context.Background(), "a", items, nil); result != nil || !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(len(result), err)
	}
}

func FuzzCompiledSourceBatch(f *testing.F) {
	f.Add(compiledSeed())
	f.Add([]byte{})
	f.Add([]byte{10, 1, 255})
	f.Fuzz(func(t *testing.T, content []byte) {
		p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{Content: content}, nil
		})}
		result, err := p.PrepareMany(context.Background(), "a", []domain.SourceSelection{{InboundTag: "selected", Email: "a"}, {InboundTag: "selected"}}, nil)
		if err != nil {
			if result != nil {
				t.Fatal("global failure returned a partial candidate")
			}
			return
		}
		if len(result) != 2 {
			t.Fatal(len(result))
		}
		for i, selected := range result {
			if selected.Err != nil {
				if selected.Configuration != nil {
					t.Fatal("failed selection retained credentials")
				}
				continue
			}
			var user protocol.User
			email := "a"
			if i == 1 {
				email = ""
			}
			if len(selected.Configuration) > domain.MaxAccountBytes || proto.Unmarshal(selected.Configuration, &user) != nil || user.Email != email || user.Account == nil {
				t.Fatal("invalid selected native account")
			}
		}
	})
}
