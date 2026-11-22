//go:build linux

package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func (r *Runner) Certificate(ctx context.Context, input domain.CertificateInput) (domain.CertificateResult, error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return domain.CertificateResult{}, err
	}
	defer release()
	args := []string{"tls", "cert", "--json=true"}
	for _, name := range input.Domains {
		args = append(args, "--domain="+name)
	}
	name, org := "Xray Inc", "Xray Inc"
	if input.CommonName != nil {
		name = *input.CommonName
	}
	if input.Organization != nil {
		org = *input.Organization
	}
	args = append(args, "--name="+name, "--org="+org)
	validity := domain.DefaultValidity
	if input.ValidFor != nil {
		validity = *input.ValidFor
	}
	args = append(args, "--expire="+validity.String())
	if input.Authority {
		args = append(args, "--ca")
	}
	started := time.Now()
	raw, err := r.run(ctx, args...)
	if err != nil {
		return domain.CertificateResult{}, err
	}
	defer clear(raw)
	var native struct {
		Certificate []string `json:"certificate"`
		Key         []string `json:"key"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&native) != nil {
		return domain.CertificateResult{}, domain.ErrInvalidOutput
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return domain.CertificateResult{}, domain.ErrInvalidOutput
	}
	certificate := strings.Join(native.Certificate, "\n") + "\n"
	private := strings.Join(native.Key, "\n") + "\n"
	pair, err := tls.X509KeyPair([]byte(certificate), []byte(private))
	if err != nil || len(pair.Certificate) != 1 {
		return domain.CertificateResult{}, domain.ErrInvalidOutput
	}
	cert := pair.Leaf
	if cert == nil {
		cert, err = x509.ParseCertificate(pair.Certificate[0])
	}
	if err != nil {
		return domain.CertificateResult{}, domain.ErrInvalidOutput
	}
	keyUsage := x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature
	if input.Authority {
		keyUsage |= x509.KeyUsageCertSign
	}
	if cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) != nil || cert.Subject.CommonName != name || !equalNames(cert.Subject.Organization, []string{org}) || !equalNames(cert.DNSNames, input.Domains) || cert.IsCA != input.Authority || cert.KeyUsage != keyUsage || cert.NotAfter.Before(started.Add(validity).Add(-2*time.Second)) || cert.NotAfter.After(time.Now().Add(validity).Add(2*time.Second)) {
		return domain.CertificateResult{}, domain.ErrInvalidOutput
	}
	return domain.CertificateResult{CertificatePEM: certificate, PrivateKeyPEM: private}, nil
}
func equalNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func (r *Runner) CertificateHashes(ctx context.Context, input domain.CertificateHashInput) (result domain.CertificateHashResult, err error) {
	ctx, release, err := r.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()

	certs, err := validators.Certificates(input.Certificate)
	if err != nil {
		return result, err
	}
	if r.config.WorkDirectory == "" {
		return result, domain.ErrNotConfigured
	}
	// This native CLI consumes a file. Stage only the bounded supplied certificate
	// in the configured private workspace; no arbitrary path or key file is used.
	directory, err := os.MkdirTemp(r.config.WorkDirectory, "credentials-")
	if err != nil {
		return result, domain.ErrUnavailable
	}
	defer func() {
		if os.RemoveAll(directory) != nil {
			result = domain.CertificateHashResult{}
			err = domain.ErrUnavailable
		}
	}()
	path := filepath.Join(directory, "certificate")
	if os.WriteFile(path, input.Certificate, 0600) != nil {
		return result, domain.ErrUnavailable
	}
	raw, err := r.run(ctx, "tls", "hash", "--cert", path)
	if err != nil {
		return result, err
	}
	defer clear(raw)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != len(certs) {
		return result, domain.ErrInvalidOutput
	}
	hashes := make([]domain.CertificateHash, len(certs))
	for i, cert := range certs {
		prefix := "Leaf SHA256:"
		if i > 0 {
			prefix = "CA <" + cert.Subject.CommonName + "> SHA256:"
		}
		value, ok := strings.CutPrefix(lines[i], prefix)
		digest := sha256.Sum256(cert.Raw)
		expected := hex.EncodeToString(digest[:])
		if !ok || strings.TrimSpace(value) != expected {
			return result, domain.ErrInvalidOutput
		}
		hashes[i] = domain.CertificateHash{Index: i, CommonName: cert.Subject.CommonName, SHA256: expected}
	}
	return domain.CertificateHashResult{Hashes: hashes}, nil
}
