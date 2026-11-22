package configuration_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/infra/configuration"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type compiler func(context.Context, string, []configs.Source) (configs.Compilation, error)

func (c compiler) Compile(ctx context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
	return c(ctx, id, sources)
}
func TestPreparationSelectsOneHandlerInOneCompilation(t *testing.T) {
	target := &core.OutboundHandlerConfig{Tag: "selected", ProxySettings: &serial.TypedMessage{Type: "opaque", Value: []byte{1, 2, 3}}}
	target.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
	content, _ := proto.Marshal(&core.Config{Outbound: []*core.OutboundHandlerConfig{{Tag: "excluded"}, target}})
	calls := 0
	p := configuration.Preparer{Compiler: compiler(func(_ context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
		calls++
		if id != "a" || len(sources) != 2 || sources[1].Name != "tail.json" {
			t.Fatal(id, sources)
		}
		return configs.Compilation{Content: content}, nil
	})}
	result, err := p.Prepare(context.Background(), "a", "selected", []configs.Source{{Name: "first.json"}, {Name: "tail.json"}})
	var actual core.OutboundHandlerConfig
	if err != nil || proto.Unmarshal(result, &actual) != nil || !proto.Equal(target, &actual) || calls != 1 {
		t.Fatal(&actual, err, calls)
	}
}
func TestPreparationRejectsAmbiguityAndCompilerFailuresWithoutOutput(t *testing.T) {
	for _, outbounds := range [][]*core.OutboundHandlerConfig{nil, {{Tag: "other"}}, {{Tag: "a"}, {Tag: "a"}}} {
		content, _ := proto.Marshal(&core.Config{Outbound: outbounds})
		p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{Content: content}, nil
		})}
		result, err := p.Prepare(context.Background(), "i", "a", nil)
		if !errors.Is(err, domain.ErrInvalidArgument) || result != nil {
			t.Fatal(result, err)
		}
	}
	for _, cause := range []error{configs.ErrRejected, configs.ErrUnsupported, configs.ErrTooLarge, context.Canceled} {
		p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
			return configs.Compilation{}, cause
		})}
		result, err := p.Prepare(context.Background(), "i", "a", nil)
		if !errors.Is(err, cause) || result != nil {
			t.Fatal(result, err)
		}
	}
	huge, _ := proto.Marshal(&core.Config{Outbound: []*core.OutboundHandlerConfig{{Tag: "a", ProxySettings: &serial.TypedMessage{Value: bytes.Repeat([]byte{1}, domain.MaxConfigurationBytes)}}}})
	p := configuration.Preparer{Compiler: compiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
		return configs.Compilation{Content: huge}, nil
	})}
	result, err := p.Prepare(context.Background(), "i", "a", nil)
	if !errors.Is(err, domain.ErrCapacity) || result != nil {
		t.Fatal(len(result), err)
	}
}
