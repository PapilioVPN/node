// Package policies owns original-source identity and equivalence rules.
package policies

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

// SourceDigest includes ordered logical names and exact source bytes. It is
// independent of compiled equivalence, which cannot represent source Env.
func SourceDigest(sources []domain.Source) string {
	hash := sha256.New()
	hash.Write([]byte("papilio-xray-sources-v1\x00"))
	var size [8]byte
	for _, source := range sources {
		binary.BigEndian.PutUint64(size[:], uint64(len(source.Name)))
		hash.Write(size[:])
		hash.Write([]byte(source.Name))
		binary.BigEndian.PutUint64(size[:], uint64(len(source.Content)))
		hash.Write(size[:])
		hash.Write(source.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}
func SameSources(a, b []domain.Source) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || !bytes.Equal(a[i].Content, b[i].Content) {
			return false
		}
	}
	return true
}
