package accounts_test

import (
	"context"
	"testing"

	source "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	codec "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type nativeRead struct{ user *protocol.User }

func (r nativeRead) Invoke(_ context.Context, _ string, _, out any, _ ...grpc.CallOption) error {
	out.(*handlers.GetInboundUserResponse).Users = []*protocol.User{r.user}
	return nil
}
func (nativeRead) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("unexpected stream")
}

func TestNativeReadbackEvidenceMatchesDesiredEnvelopeWithoutReconstruction(t *testing.T) {
	for _, kind := range []string{"xray.proxy.vless.Account", "xray.proxy.wireguard.PeerConfig", "opaque.Account"} {
		for _, unknown := range []bool{false, true} {
			user := &protocol.User{Email: "Exact", Level: 7, Account: &serial.TypedMessage{Type: kind, Value: []byte{1, 2, 3}}}
			if unknown {
				user.Account.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 7})
			}
			record, err := source.New(nativeRead{user}).Get(context.Background(), "members", user.Email)
			if err != nil {
				t.Fatal(err)
			}
			observed, err := (codec.Codec{}).Observe(*record)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := proto.Marshal(user)
			desired, err := (codec.Codec{}).Decode(raw)
			if err != nil || observed.Fingerprint != desired.Fingerprint || observed.Comparable != desired.Comparable || observed.Email != desired.Email || observed.AccountType != desired.AccountType {
				t.Fatal(observed.Comparable, desired.Comparable, err)
			}
			if &observed.Configuration[0] != &record.Configuration[0] {
				t.Fatal("trusted export credentials copied again")
			}
		}
	}
}
