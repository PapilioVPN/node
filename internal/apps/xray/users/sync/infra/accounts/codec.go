// Package accounts adapts native account records and the existing Accounts API.
package accounts

import (
	"bytes"
	"crypto/sha256"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

type Codec struct{}

// Decode fingerprints the official User envelope deterministically. It does not
// advertise equality for known lossy managers or unknown fields. No credential
// digest is exposed in a public preview or receipt.
func (Codec) Decode(raw []byte) (domain.Member, error) {
	var u protocol.User
	if err := proto.Unmarshal(raw, &u); err != nil || u.Account == nil || u.Account.Type == "" {
		return domain.Member{}, domain.ErrInvalid
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(&u)
	if err != nil {
		return domain.Member{}, domain.ErrInvalid
	}
	comparable := false
	switch u.Account.Type {
	case "xray.proxy.vless.Account", "xray.proxy.vmess.Account", "xray.proxy.trojan.Account", "xray.proxy.shadowsocks_2022.Account", "xray.proxy.hysteria.account.Account", "xray.proxy.masque.Account":
		comparable = len(u.ProtoReflect().GetUnknown()) == 0 && len(u.Account.ProtoReflect().GetUnknown()) == 0
	}
	return domain.Member{Email: u.Email, AccountType: u.Account.Type, Configuration: bytes.Clone(raw), Comparable: comparable, Fingerprint: sha256.Sum256(canonical)}, nil
}
