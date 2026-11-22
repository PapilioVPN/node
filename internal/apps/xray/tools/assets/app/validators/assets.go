// Package validators owns pure asset envelope and identity checks.
package validators

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"strings"
	"unicode/utf8"
)

func ID(s string) bool {
	return len(s) > 0 && len(s) <= 128 && utf8.ValidString(s) && strings.TrimSpace(s) != "" && !strings.ContainsRune(s, 0)
}
func Digest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func Candidate(c domain.Candidate) error {
	for _, v := range []struct {
		f   domain.File
		max int
	}{{c.Binary, domain.MaxBinary}, {c.GeoIP, domain.MaxGeodata}, {c.GeoSite, domain.MaxGeodata}} {
		if len(v.f.Content) > v.max {
			return domain.ErrCapacity
		}
		if len(v.f.Content) == 0 || !Digest(v.f.SHA256) {
			return domain.ErrInvalidArgument
		}
		sum := sha256.Sum256(v.f.Content)
		if hex.EncodeToString(sum[:]) != v.f.SHA256 {
			return domain.ErrRejected
		}
	}
	return nil
}
