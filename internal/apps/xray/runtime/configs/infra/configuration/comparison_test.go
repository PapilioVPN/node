package configuration

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func compiled(t *testing.T, tag string) []byte {
	t.Helper()
	b, err := proto.Marshal(&core.Config{App: []*serial.TypedMessage{serial.ToTypedMessage(&router.Config{Rule: []*router.RoutingRule{{TargetTag: &router.RoutingRule_Tag{Tag: tag}}}}), serial.ToTypedMessage(&commander.Config{Service: []*serial.TypedMessage{{Type: "xray.app.router.command.Config"}}})}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestComparisonPreservesSourceEnvironmentAndNativeInitialization(t *testing.T) {
	sources := []domain.Source{{Name: "base.json", Content: []byte(`{"env":{"key":"secret"}}`)}}
	for _, test := range []struct {
		name    string
		content string
		file    string
		mode    domain.ApplyMode
	}{
		{"same env", "{\"Env\":{\"key\":\"secret\"}}", "base.json", domain.LiveRouting},
		{"changed env", `{"env":{"key":"other"}}`, "base.json", domain.Restart},
		{"unknown yaml context", "log: {}", "base.yaml", domain.Restart},
	} {
		t.Run(test.name, func(t *testing.T) {
			out, err := (Inspector{}).Compare(context.Background(), sources, []domain.Source{{Name: test.file, Content: []byte(test.content)}}, compiled(t, "old"), compiled(t, "new"))
			if err != nil || out.Mode != test.mode {
				t.Fatal(out, err)
			}
		})
	}
	out, err := (Inspector{}).Compare(context.Background(), sources, sources, compiled(t, "old"), compiled(t, "old"))
	if err != nil || out.Mode != domain.Unchanged {
		t.Fatal(out, err)
	}
	var changed core.Config
	proto.Unmarshal(compiled(t, "new"), &changed)
	changed.App[0].ProtoReflect().SetUnknown([]byte{0x18, 0x01})
	b, _ := proto.Marshal(&changed)
	out, err = (Inspector{}).Compare(context.Background(), sources, sources, compiled(t, "old"), b)
	if err != nil || out.Mode != domain.Restart {
		t.Fatal("unapplied envelope fields became live", out, err)
	}
	routing := &router.Config{}
	proto.Unmarshal(changed.App[0].Value, routing)
	routing.Script = "return 1"
	changed.App[0] = serial.ToTypedMessage(routing)
	b, _ = proto.Marshal(&changed)
	out, err = (Inspector{}).Compare(context.Background(), sources, sources, compiled(t, "old"), b)
	if err != nil || out.Mode != domain.Restart {
		t.Fatal("router initialization became live", out, err)
	}
}

func TestUnusedOutboundMetadataDoesNotRequireExecutionReplacement(t *testing.T) {
	sources := []domain.Source{{Name: "base.pb"}}
	a := &core.Config{Outbound: []*core.OutboundHandlerConfig{{Tag: "direct", Comment: "old"}}}
	b := proto.Clone(a).(*core.Config)
	b.Outbound[0].Comment = "new private operator note"
	b.Outbound[0].Expire = 123
	old, _ := proto.Marshal(a)
	next, _ := proto.Marshal(b)
	result, err := (Inspector{}).Compare(context.Background(), sources, sources, old, next)
	if err != nil || result.Mode != domain.Unchanged {
		t.Fatal(result, err)
	}
	if a.Outbound[0].Comment != "old" || b.Outbound[0].Expire != 123 {
		t.Fatal("comparison mutated input")
	}
}
