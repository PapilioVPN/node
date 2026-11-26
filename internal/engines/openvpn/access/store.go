// Package access manages certificate revocation lists for an OpenVPN server.
package access

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

type Store struct {
	CAFile string
	Path   string
}

func (s Store) Read() (lifecycle.RevocationStatus, error) {
	source, list, err := s.current()
	if err != nil {
		return lifecycle.RevocationStatus{}, err
	}
	return describe(source, list), nil
}

func (s Store) current() ([]byte, *x509.RevocationList, error) {
	ca, err := s.issuer()
	if err != nil {
		return nil, nil, err
	}
	source, _, err := read(s.Path)
	if err != nil {
		return nil, nil, errors.Join(lifecycle.ErrRevocationUnavailable, err)
	}
	list, err := parse(source, ca)
	if err != nil {
		return nil, nil, errors.Join(lifecycle.ErrRevocationUnavailable, err)
	}
	return source, list, nil
}

func (s Store) ValidateCurrent() error {
	_, current, err := s.current()
	if err != nil {
		return err
	}
	now := time.Now()
	if !now.Before(current.NextUpdate) || current.ThisUpdate.After(now) {
		return lifecycle.ErrRevocationUnavailable
	}
	return nil
}

func (s Store) Replace(ctx context.Context, source []byte) (lifecycle.RevocationStatus, error) {
	if err := ctx.Err(); err != nil {
		return lifecycle.RevocationStatus{}, err
	}
	ca, err := s.issuer()
	if err != nil {
		return lifecycle.RevocationStatus{}, err
	}
	list, err := parse(source, ca)
	now := time.Now()
	if err != nil || !now.Before(list.NextUpdate) || list.ThisUpdate.After(now) {
		return lifecycle.RevocationStatus{}, lifecycle.ErrRevocationRejected
	}
	result := describe(source, list)
	current, modified, err := read(s.Path)
	switch {
	case err == nil:
		old, err := parse(current, ca)
		if err != nil {
			return lifecycle.RevocationStatus{}, lifecycle.ErrRevocationUnavailable
		}
		if bytes.Equal(source, current) {
			return result, nil
		}
		if list.Number.Cmp(old.Number) <= 0 || list.ThisUpdate.Before(old.ThisUpdate) {
			return lifecycle.RevocationStatus{}, lifecycle.ErrRevocationRejected
		}
		// OpenVPN keys the reload by size and whole-second mtime. Force a
		// different second even for same-sized lists published in one second.
		if now.Unix() <= modified.Unix() {
			now = time.Unix(modified.Unix()+1, 0)
		}
	case !errors.Is(err, os.ErrNotExist):
		return lifecycle.RevocationStatus{}, errors.Join(lifecycle.ErrRevocationUnavailable, err)
	}
	if err := write(ctx, s.Path, source, now); err != nil {
		return lifecycle.RevocationStatus{}, errors.Join(lifecycle.ErrRevocationUnavailable, err)
	}
	return result, nil
}

func (s Store) issuer() (*x509.Certificate, error) {
	b, _, err := read(s.CAFile)
	if err != nil {
		return nil, errors.Join(lifecycle.ErrRevocationUnavailable, err)
	}
	block, rest := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, lifecycle.ErrRevocationUnavailable
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !ca.IsCA || ca.KeyUsage&x509.KeyUsageCRLSign == 0 {
		return nil, lifecycle.ErrRevocationUnavailable
	}
	return ca, nil
}

func parse(source []byte, ca *x509.Certificate) (*x509.RevocationList, error) {
	block, rest := pem.Decode(source)
	if block == nil || block.Type != "X509 CRL" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, lifecycle.ErrRevocationRejected
	}
	list, err := x509.ParseRevocationList(block.Bytes)
	if err != nil || list.Number == nil || list.Number.Sign() < 0 ||
		!list.NextUpdate.After(list.ThisUpdate) || !bytes.Equal(list.RawIssuer, ca.RawSubject) || list.CheckSignatureFrom(ca) != nil {
		return nil, lifecycle.ErrRevocationRejected
	}
	return list, nil
}

func describe(source []byte, list *x509.RevocationList) lifecycle.RevocationStatus {
	sum := sha256.Sum256(source)
	return lifecycle.RevocationStatus{
		Number: list.Number.String(), SHA256: hex.EncodeToString(sum[:]),
		NextUpdate: list.NextUpdate.Unix(), RevokedCount: len(list.RevokedCertificateEntries),
	}
}

func read(path string) ([]byte, time.Time, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, time.Time{}, err
	}
	if !info.Mode().IsRegular() {
		return nil, time.Time{}, lifecycle.ErrRevocationUnavailable
	}
	source, err := io.ReadAll(io.LimitReader(file, lifecycle.MaxRevocationBytes+1))
	if err == nil && len(source) > lifecycle.MaxRevocationBytes {
		err = lifecycle.ErrRevocationUnavailable
	}
	return source, info.ModTime(), err
}

func write(ctx context.Context, path string, source []byte, modified time.Time) error {
	parent := filepath.Dir(path)
	file, err := os.CreateTemp(parent, ".openvpn-crl-")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err := file.Write(source); err != nil {
		return err
	}
	// The signed list contains serials only. The OpenVPN runtime user
	// may differ from Node after OpenVPN drops privileges.
	if err := file.Chmod(0644); err != nil {
		return err
	}
	if err := os.Chtimes(file.Name(), modified, modified); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return directory.Sync()
}
