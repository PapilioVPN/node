package configuration

import (
	"context"
	"testing"

	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestInspectionBindsNativeProjectionAndRejectsIgnoredFields(t *testing.T) {
	config := &core.OutboundHandlerConfig{Tag: " A>>>B ", ProxySettings: &serial.TypedMessage{Type: "opaque.Type", Value: []byte{1, 2, 3}}}
	content, _ := proto.Marshal(config)
	original, err := (Inspector{}).Inspect(context.Background(), content)
	if err != nil || original.Tag != config.Tag || len(original.Fingerprint) != 64 {
		t.Fatal(original, err)
	}
	unknown := protowire.AppendTag(nil, 100, protowire.VarintType)
	config.ProtoReflect().SetUnknown(protowire.AppendVarint(unknown, 42))
	content, _ = proto.Marshal(config)
	changed, err := (Inspector{}).Inspect(context.Background(), content)
	if err != nil || original.Fingerprint == changed.Fingerprint {
		t.Fatal("unknown field not bound", changed, err)
	}
	for _, field := range []string{"expire", "comment"} {
		config.Expire = 0
		config.Comment = ""
		if field == "expire" {
			config.Expire = 1
		} else {
			config.Comment = "unused"
		}
		content, _ = proto.Marshal(config)
		if _, err := (Inspector{}).Inspect(context.Background(), content); err == nil {
			t.Fatal("ignored native field accepted as effective", field)
		}
	}
}
