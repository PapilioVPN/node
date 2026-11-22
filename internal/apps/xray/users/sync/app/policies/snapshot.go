package policies

import (
	"crypto/sha256"
	"sort"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

// SameObservation compares unordered native membership, including duplicates,
// without assuming native map enumeration order. It never exports the digest.
func SameObservation(a, b domain.Snapshot) bool { return observationDigest(a) == observationDigest(b) }
func observationDigest(s domain.Snapshot) [32]byte {
	h := sha256.New()
	number(h, s.Revision.Config)
	number(h, s.Revision.Generation)
	ins := append([]domain.Inbound(nil), s.Inbounds...)
	sort.Slice(ins, func(i, j int) bool { return ins[i].Tag < ins[j].Tag })
	number(h, uint64(len(ins)))
	for _, in := range ins {
		for _, value := range []string{in.Tag, in.AccountType, in.EmailMatching} {
			field(h, []byte(value))
		}
		if in.Live {
			number(h, 1)
		} else {
			number(h, 0)
		}
		users := make([][32]byte, len(in.Users))
		for i, u := range in.Users {
			users[i] = u.Fingerprint
		}
		sort.Slice(users, func(i, j int) bool { return string(users[i][:]) < string(users[j][:]) })
		number(h, uint64(len(users)))
		for _, user := range users {
			field(h, user[:])
		}
	}
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}
