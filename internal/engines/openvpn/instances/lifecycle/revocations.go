package lifecycle

import (
	"context"
	"errors"
	"time"
)

const MaxRevocationBytes = 1 << 20

var (
	ErrRevocationDisabled    = errors.New("managed OpenVPN revocations are disabled")
	ErrRevocationRejected    = errors.New("OpenVPN revocation list was rejected")
	ErrRevocationUnavailable = errors.New("OpenVPN revocation list is unavailable")
)

type RevocationStatus struct {
	Number       string `json:"number"`
	SHA256       string `json:"sha256"`
	NextUpdate   int64  `json:"next_update"`
	RevokedCount int    `json:"revoked_count"`
}

type Revocations interface {
	Read() (RevocationStatus, error)
	Replace(context.Context, []byte) (RevocationStatus, error)
}

func (s *Service) ReadRevocationList(id string) (RevocationStatus, error) {
	if !s.matches(id) {
		return RevocationStatus{}, ErrNotFound
	}
	if s.revocations == nil {
		return RevocationStatus{}, ErrRevocationDisabled
	}
	return s.revocations.Read()
}

func (s *Service) ReplaceRevocationList(ctx context.Context, id string, source []byte) (RevocationStatus, error) {
	work, done, err := s.begin(ctx, id, 4*time.Second)
	if err != nil {
		return RevocationStatus{}, err
	}
	defer done()
	if s.revocations == nil {
		return RevocationStatus{}, ErrRevocationDisabled
	}
	if len(source) > MaxRevocationBytes {
		return RevocationStatus{}, ErrTooLarge
	}
	return s.revocations.Replace(work, source)
}
