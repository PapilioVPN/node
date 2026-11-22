// Package domain defines request-scoped native Xray credential artifacts.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument = errors.New("invalid credential request")
	ErrNotFound        = errors.New("Xray instance not found")
	ErrNotConfigured   = errors.New("credential facility not configured")
	ErrCapacity        = errors.New("credential capacity exceeded")
	ErrClosed          = errors.New("credential execution owner closed")
	ErrUnavailable     = errors.New("native credential tool unavailable")
	ErrRejected        = errors.New("native credential tool rejected request")
	ErrInvalidOutput   = errors.New("invalid native credential output")
)

const (
	MaxRequestBytes     = 128 << 10
	MaxOutputBytes      = 128 << 10
	MaxCertificateBytes = 64 << 10
	MaxCertificates     = 16
	MaxECHBytes         = 32 << 10
	MaxECHKeys          = 16
	MaxDomains          = 32
	MaxParallel         = 4
	MaxInstanceParallel = 2
	ExecutionTimeout    = 5 * time.Second
	DefaultValidity     = 90 * 24 * time.Hour
	MaxValidity         = 3650 * 24 * time.Hour
)

type Encoding string

const (
	RawURL   Encoding = "base64url"
	Standard Encoding = "base64"
)

type ECHFormat string

const (
	Base64 ECHFormat = "base64"
	PEM    ECHFormat = "pem"
)

// A nil optional input requests fresh randomness. A present empty value is invalid.
type UUIDInput struct{ Name *string }
type X25519Input struct {
	PrivateKey *string
	Encoding   Encoding
}
type WireGuardInput struct{ PrivateKey *string }
type SeedInput struct{ Seed *string }
type CertificateInput struct {
	Domains      []string
	CommonName   *string
	Organization *string
	Authority    bool
	ValidFor     *time.Duration
}
type CertificateHashInput struct{ Certificate []byte }
type ECHInput struct {
	ServerName *string
	ServerKeys *string
	Format     ECHFormat
}
type Metadata struct {
	InstanceID, Tool string
	CompletedAt      time.Time
	Duration         time.Duration
}
type UUIDResult struct {
	Metadata
	UUID string
}
type CurveResult struct {
	Metadata
	PrivateKey, PublicKey, Hash32 string
	Encoding                      Encoding
}
type MLDSA65Result struct {
	Metadata
	Seed, Verify string
}
type MLKEM768Result struct {
	Metadata
	Seed, Client, Hash32 string
}
type AuthenticationPair struct{ Decryption, Encryption string }
type VLESSResult struct {
	Metadata
	X25519, MLKEM768 AuthenticationPair
}
type CertificateResult struct {
	Metadata
	CertificatePEM, PrivateKeyPEM string
}
type CertificateHash struct {
	Index              int
	CommonName, SHA256 string
}
type CertificateHashResult struct {
	Metadata
	Hashes []CertificateHash
}
type ECHResult struct {
	Metadata
	ConfigLists []string
	ServerKeys  string
	Format      ECHFormat
}
