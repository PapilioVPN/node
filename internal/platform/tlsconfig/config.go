// Package tlsconfig loads explicit trust roots without altering host trust or files.
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

func certificateAndRoots(certificateFile, keyFile, caFile string) (tls.Certificate, *x509.CertPool, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load TLS certificate: %w", err)
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return tls.Certificate{}, nil, errors.New("TLS CA contains no valid certificates")
	}
	return certificate, roots, nil
}

// Server requires a verified client chain as well as presenting its own certificate.
func Server(certificateFile, keyFile, caFile string) (*tls.Config, error) {
	certificate, roots, err := certificateAndRoots(certificateFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, nil
}

// Client presents its certificate and verifies the broker using explicit roots.
// The native NATS client supplies the server name from the configured URL.
func Client(certificateFile, keyFile, caFile string) (*tls.Config, error) {
	certificate, roots, err := certificateAndRoots(certificateFile, keyFile, caFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, RootCAs: roots}, nil
}
