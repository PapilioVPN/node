package configuration

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	configdomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type compilerFunc func(context.Context, string, []configdomain.Source) (configdomain.Compilation, error)

func (f compilerFunc) Compile(c context.Context, id string, s []configdomain.Source) (configdomain.Compilation, error) {
	return f(c, id, s)
}
func TestSingleCompilationAndUnknownNativeFieldsArePreserved(t *testing.T) {
	native := serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{{RuleTag: "opaque"}}})
	native.Value = append(native.Value, 0xf8, 0x7f, 0x01)
	root, _ := proto.Marshal(&core.Config{App: []*serial.TypedMessage{native}})
	calls := 0
	p := Preparer{Compiler: compilerFunc(func(_ context.Context, id string, s []configdomain.Source) (configdomain.Compilation, error) {
		calls++
		if id != "one" || len(s) != 1 || s[0].Name != "routing.json" {
			t.Fatal(id, s)
		}
		return configdomain.Compilation{Content: root}, nil
	})}
	out, err := p.Prepare(context.Background(), "one", []byte(`{"type":"field","ruleTag":"opaque","outboundTag":"direct"}`))
	if err != nil || calls != 1 || !proto.Equal(native, &serial.TypedMessage{Type: native.Type, Value: out}) {
		t.Fatal(out, calls, err)
	}
	out[0] ^= 0xff
	if native.Value[0] == out[0] {
		t.Fatal("prepared result borrowed compiler memory")
	}
}
func TestMalformedCompiledConfigurationHasNoCandidate(t *testing.T) {
	for _, apps := range [][]*serial.TypedMessage{nil, {serial.ToTypedMessage(&router.Config{})}, {serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{{}}}), serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{{}}})}} {
		data, _ := proto.Marshal(&core.Config{App: apps})
		out, err := Extract(context.Background(), data)
		if !errors.Is(err, domain.ErrInvalidResponse) || out != nil {
			t.Fatal(out, err)
		}
	}
}
