// Package mtls loads mutual-TLS identities for native Go transports.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// Files identifies a local certificate, its private key and trusted peer CA roots.
type Files struct {
	Certificate string `yaml:"certificate"`
	PrivateKey  string `yaml:"private_key"`
	CA          string `yaml:"ca"`
}

// Server requires clients to present a certificate signed by the configured CA.
func Server(files Files) (*tls.Config, error) {
	identity, roots, err := load(files)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{identity},
		ClientCAs:    roots,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}, nil
}

// Client verifies the server and presents its own certificate. The transport
// supplies the peer's DNS name through the native TLS ServerName contract.
func Client(files Files) (*tls.Config, error) {
	identity, roots, err := load(files)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{identity},
		RootCAs:      roots,
	}, nil
}

func load(files Files) (tls.Certificate, *x509.CertPool, error) {
	identity, err := tls.LoadX509KeyPair(files.Certificate, files.PrivateKey)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("load TLS identity: %w", err)
	}
	contents, err := os.ReadFile(files.CA)
	if err != nil {
		return tls.Certificate{}, nil, fmt.Errorf("read TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(contents) {
		return tls.Certificate{}, nil, errors.New("TLS CA file contains no valid certificates")
	}
	return identity, roots, nil
}
