package policies_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	codec "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"google.golang.org/protobuf/proto"
)

func raw(email, secret, kind string) []byte {
	b, _ := proto.Marshal(&protocol.User{Email: email, Account: &serial.TypedMessage{Type: kind, Value: []byte(secret)}})
	return b
}

const vless = "xray.proxy.vless.Account"

func request(mode domain.Mode, users ...[]byte) domain.Request {
	if users == nil {
		users = [][]byte{}
	}
	return domain.Request{InstanceID: "one", OperationID: "op", Expected: domain.Revision{Config: 7, Generation: 3}, Mode: mode, Selection: []string{"selected"}, Sets: []accounts.InboundUserSet{{InboundTag: "selected", Accounts: users}}}
}
func snapshot(t *testing.T, kind string, users ...[]byte) domain.Snapshot {
	t.Helper()
	in := domain.Inbound{Tag: "selected", AccountType: kind, EmailMatching: "lowercase", Live: true}
	for _, raw := range users {
		m, err := (codec.Codec{}).Decode(raw)
		if err != nil {
			t.Fatal(err)
		}
		in.Users = append(in.Users, m)
	}
	return domain.Snapshot{Revision: domain.Revision{Config: 7, Generation: 3}, Inbounds: []domain.Inbound{in}}
}
func TestDeltaPlanAndCredentialFreePreview(t *testing.T) {
	a, b := raw("a", "secret-old", vless), raw("b", "secret-b", vless)
	r := request(domain.Delta, raw("a", "secret-new", vless), b, raw("c", "secret-c", vless))
	p, err := policies.Build(context.Background(), r, snapshot(t, vless, a, b), codec.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Steps) != 3 || p.Steps[0].Action != domain.Remove || p.Steps[0].Email != "a" || p.Steps[1].Action != domain.Add || p.Steps[2].Email != "c" {
		t.Fatalf("steps: %+v", p.Steps)
	}
	d := p.Preview.Differences[0]
	if d.Added != 1 || d.Replaced != 1 || d.Unchanged != 1 {
		t.Fatalf("difference: %+v", d)
	}
	bts, _ := json.Marshal(p.Preview)
	if strings.Contains(string(bts), "secret") || strings.Contains(string(bts), "Fingerprint") || strings.Contains(string(bts), "Configuration") {
		t.Fatal("preview leaks private account material")
	}
}
func TestFullEmptyAndExcluded(t *testing.T) {
	r := request(domain.Full)
	p, err := policies.Build(context.Background(), r, snapshot(t, vless, raw("a", "old", vless)), codec.Codec{})
	if err != nil || len(p.Steps) != 1 || p.Preview.Differences[0].Removed != 1 {
		t.Fatalf("clear: %+v %v", p.Preview, err)
	}
	r.Sets = append(r.Sets, accounts.InboundUserSet{InboundTag: "excluded", Accounts: [][]byte{}})
	if err = policies.Validate(context.Background(), r); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("excluded: %v", err)
	}
	r = request(domain.Full)
	r.Sets[0].Accounts = nil
	if err = policies.Validate(context.Background(), r); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing membership: %v", err)
	}
}
func TestStaleAliasesAndLossyWireGuard(t *testing.T) {
	r := request(domain.Delta, raw("A", "new", vless), raw("a", "other", vless))
	if _, err := policies.Build(context.Background(), r, snapshot(t, vless), codec.Codec{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("aliases: %v", err)
	}
	r = request(domain.Delta)
	r.Expected.Config--
	if _, err := policies.Build(context.Background(), r, snapshot(t, vless), codec.Codec{}); !errors.Is(err, domain.ErrStale) {
		t.Fatalf("stale: %v", err)
	}
	wg := "xray.proxy.wireguard.PeerConfig"
	r = request(domain.Delta, raw("same", "key-and-psk", wg))
	s := snapshot(t, wg, r.Sets[0].Accounts[0], raw("same", "old-key", wg))
	s.Inbounds[0].EmailMatching = "exact"
	p, err := policies.Build(context.Background(), r, s, codec.Codec{})
	if err != nil || len(p.Steps) != 3 || p.Steps[0].Action != domain.Remove || p.Steps[1].Action != domain.Remove || p.Steps[2].Action != domain.Add {
		t.Fatalf("WG replacement: %+v %v", p.Steps, err)
	}
}

func TestFullPreservesAnonymousAndNativeDuplicateMembership(t *testing.T) {
	wg := "xray.proxy.wireguard.PeerConfig"
	r := request(domain.Full, raw("same", "first-key", wg), raw("same", "second-key", wg), raw("", "anonymous-key", wg))
	s := snapshot(t, wg)
	s.Inbounds[0].EmailMatching = "exact"
	p, err := policies.Build(context.Background(), r, s, codec.Codec{})
	if err != nil || p.Preview.Differences[0].Added != 3 || len(p.Request.Sets[0].Accounts) != 3 {
		t.Fatal(p.Preview, err)
	}
	r = request(domain.Delta, raw("named", "credential", vless))
	s = snapshot(t, vless, raw("", "unselectable", vless))
	if _, err := policies.Build(context.Background(), r, s, codec.Codec{}); err != nil {
		t.Fatalf("unselected anonymous account blocks named delta: %v", err)
	}
}
