package api_test

import (
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestPreparedAccountFollowsNativeSingularMessageMerge(t *testing.T) {
	raw, _ := proto.Marshal(&protocol.User{Email: "email", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: []byte("private-credential")}})
	fragment := protowire.AppendBytes(protowire.AppendTag(nil, 2, protowire.BytesType), []byte("replacement-credential"))
	raw = protowire.AppendBytes(protowire.AppendTag(raw, 3, protowire.BytesType), fragment)
	actual, err := (api.Client{}).Prepare(raw)
	var native protocol.User
	if proto.Unmarshal(raw, &native) != nil || err != nil || actual.AccountType() != native.Account.Type {
		t.Fatal(actual, err)
	}
	for _, bad := range [][]byte{nil, {255}, protowire.AppendString(protowire.AppendTag(nil, 3, protowire.BytesType), "invalid")} {
		if _, err := (api.Client{}).Prepare(bad); err == nil {
			t.Fatal("malformed account accepted")
		}
	}
}

func FuzzAccountPreparation(f *testing.F) {
	f.Add([]byte{255})
	valid, _ := proto.Marshal(&protocol.User{Email: "test@example.test", Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account"}})
	f.Add(valid)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = (api.Client{}).Prepare(data)
	})
}
