package policies

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"strings"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

// Own copies retained request data at the borrowed transport-buffer boundary.
func Own(r domain.Request) domain.Request {
	r.InstanceID, r.OperationID = strings.Clone(r.InstanceID), strings.Clone(r.OperationID)
	r.Expected.Epoch = strings.Clone(r.Expected.Epoch)
	r.Selection = append([]string(nil), r.Selection...)
	for i := range r.Selection {
		r.Selection[i] = strings.Clone(r.Selection[i])
	}
	sets := make([]accounts.InboundUserSet, len(r.Sets))
	for i, set := range r.Sets {
		sets[i] = accounts.InboundUserSet{InboundTag: strings.Clone(set.InboundTag)}
		if set.Accounts != nil {
			sets[i].Accounts = make([][]byte, len(set.Accounts))
		}
		for j, raw := range set.Accounts {
			sets[i].Accounts[j] = bytes.Clone(raw)
		}
	}
	r.Sets = sets
	r.Removals = append([]accounts.Removal(nil), r.Removals...)
	for i := range r.Removals {
		r.Removals[i].InboundTag = strings.Clone(r.Removals[i].InboundTag)
		r.Removals[i].Email = strings.Clone(r.Removals[i].Email)
	}
	return r
}

func field(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}
func number(h hash.Hash, n uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	_, _ = h.Write(b[:])
}

// Digest binds order, explicit membership and exact bytes. It is private replay
// evidence, never a credential-derived public approval token.
func Digest(r domain.Request) [32]byte {
	h := sha256.New()
	for _, s := range []string{r.InstanceID, r.OperationID, string(r.Mode), r.Expected.Epoch} {
		field(h, []byte(s))
	}
	for _, n := range []uint64{r.Expected.Control, r.Expected.Config, r.Expected.Generation, uint64(len(r.Selection))} {
		number(h, n)
	}
	for _, s := range r.Selection {
		field(h, []byte(s))
	}
	number(h, uint64(len(r.Sets)))
	for _, set := range r.Sets {
		field(h, []byte(set.InboundTag))
		number(h, uint64(len(set.Accounts)))
		for _, raw := range set.Accounts {
			field(h, raw)
		}
	}
	number(h, uint64(len(r.Removals)))
	for _, r := range r.Removals {
		field(h, []byte(r.InboundTag))
		field(h, []byte(r.Email))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
