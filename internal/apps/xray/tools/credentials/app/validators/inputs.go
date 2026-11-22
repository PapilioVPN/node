// Package validators validates native tool inputs before child admission.
package validators

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func UUID(r domain.UUIDInput) error {
	if r.Name != nil && (len(*r.Name) == 0 || len(*r.Name) > 30 || !text(*r.Name, 30)) {
		return domain.ErrInvalidArgument
	}
	return nil
}
func Secret(value *string, encoding *base64.Encoding, size int) error {
	if value == nil {
		return nil
	}
	raw, err := encoding.Strict().DecodeString(*value)
	if err != nil || len(raw) != size || encoding.EncodeToString(raw) != *value {
		return domain.ErrInvalidArgument
	}
	return nil
}
func X25519(r domain.X25519Input) (domain.X25519Input, error) {
	if r.Encoding == "" {
		r.Encoding = domain.RawURL
	}
	encoding := base64.RawURLEncoding
	switch r.Encoding {
	case domain.RawURL:
	case domain.Standard:
		encoding = base64.StdEncoding
	default:
		return r, domain.ErrInvalidArgument
	}
	return r, Secret(r.PrivateKey, encoding, 32)
}
func WireGuard(r domain.WireGuardInput) error { return Secret(r.PrivateKey, base64.StdEncoding, 32) }
func Seed(r domain.SeedInput, size int) error { return Secret(r.Seed, base64.RawURLEncoding, size) }
func text(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func DNS(name string, wildcard bool) bool {
	if wildcard && strings.HasPrefix(name, "*.") {
		name = name[2:]
	}
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	name = strings.TrimSuffix(name, ".")
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func Certificate(r domain.CertificateInput) error {
	if len(r.Domains) > domain.MaxDomains {
		return domain.ErrInvalidArgument
	}
	for _, name := range r.Domains {
		if !DNS(name, true) {
			return domain.ErrInvalidArgument
		}
	}
	for _, value := range []*string{r.CommonName, r.Organization} {
		if value != nil && !text(*value, 1024) {
			return domain.ErrInvalidArgument
		}
	}
	if r.ValidFor != nil && (*r.ValidFor <= 0 || *r.ValidFor > domain.MaxValidity) {
		return domain.ErrInvalidArgument
	}
	return nil
}

// Certificates parses only bounded certificate material. No caller paths or
// private-key blocks are accepted. Native hash labels cannot frame control text.
func Certificates(data []byte) ([]*x509.Certificate, error) {
	if len(data) == 0 || len(data) > domain.MaxCertificateBytes {
		return nil, domain.ErrInvalidArgument
	}
	var certs []*x509.Certificate
	if bytes.Contains(data, []byte("BEGIN")) {
		for len(bytes.TrimSpace(data)) > 0 {
			data = bytes.TrimSpace(data)
			if !bytes.HasPrefix(data, []byte("-----BEGIN CERTIFICATE-----")) {
				return nil, domain.ErrInvalidArgument
			}
			block, rest := pem.Decode(data)
			if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
				return nil, domain.ErrInvalidArgument
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, domain.ErrInvalidArgument
			}
			certs = append(certs, cert)
			data = rest
			if len(certs) > domain.MaxCertificates {
				return nil, domain.ErrInvalidArgument
			}
		}
	} else {
		var err error
		certs, err = x509.ParseCertificates(data)
		if err != nil {
			return nil, domain.ErrInvalidArgument
		}
	}
	if len(certs) == 0 || len(certs) > domain.MaxCertificates {
		return nil, domain.ErrInvalidArgument
	}
	for _, cert := range certs {
		if strings.IndexFunc(cert.Subject.CommonName, unicode.IsControl) >= 0 {
			return nil, domain.ErrInvalidArgument
		}
	}
	return certs, nil
}
func ECH(r domain.ECHInput) (domain.ECHInput, error) {
	if r.Format == "" {
		r.Format = domain.Base64
	}
	if r.Format != domain.Base64 && r.Format != domain.PEM {
		return r, domain.ErrInvalidArgument
	}
	if r.ServerKeys != nil {
		if r.ServerName != nil {
			return r, domain.ErrInvalidArgument
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(*r.ServerKeys)
		if err != nil || len(raw) == 0 || len(raw) > domain.MaxECHBytes || base64.StdEncoding.EncodeToString(raw) != *r.ServerKeys {
			return r, domain.ErrInvalidArgument
		}
	} else if r.ServerName != nil && !DNS(*r.ServerName, false) {
		return r, domain.ErrInvalidArgument
	}
	return r, nil
}
