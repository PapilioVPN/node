// Package accounts adapts native account records and the existing Accounts API.
package accounts

import (
	"crypto/sha256"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

type Codec struct{}

// Decode borrows owned, immutable request bytes and deterministically
// fingerprints the official User envelope. Lossy managers and unknown fields
// cannot establish equality. Credential digests stay out of public results.
func (Codec) Decode(raw []byte) (domain.Member, error) {
	var u protocol.User
	if err := proto.Unmarshal(raw, &u); err != nil || u.Account == nil || u.Account.Type == "" {
		return domain.Member{}, domain.ErrInvalid
	}
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(&u)
	if err != nil {
		return domain.Member{}, domain.ErrInvalid
	}
	return domain.Member{Email: u.Email, AccountType: u.Account.Type, Configuration: raw, Comparable: comparable(u.Account.Type, len(u.ProtoReflect().GetUnknown()) != 0 || len(u.Account.ProtoReflect().GetUnknown()) != 0), Fingerprint: sha256.Sum256(canonical)}, nil
}

// Observe consumes Accounts' owned canonical native readback and metadata. It
// does not decode/re-encode a trusted internal export or copy its credentials.
func (Codec) Observe(user upstream.Record) (domain.Member, error) {
	if user.AccountType == "" || len(user.Configuration) == 0 {
		return domain.Member{}, domain.ErrInvalid
	}
	return domain.Member{Email: user.Email, AccountType: user.AccountType, Configuration: user.Configuration, Comparable: comparable(user.AccountType, user.HasUnknownFields), Fingerprint: sha256.Sum256(user.Configuration)}, nil
}
func comparable(kind string, unknown bool) bool {
	switch kind {
	case "xray.proxy.vless.Account", "xray.proxy.vmess.Account", "xray.proxy.trojan.Account", "xray.proxy.shadowsocks_2022.Account", "xray.proxy.hysteria.account.Account", "xray.proxy.masque.Account":
		return !unknown
	default:
		return false
	}
}
