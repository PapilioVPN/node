package bootstrap

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"github.com/PapilioVPN/node/internal/settings"
)

func loadTLS(config settings.Management) (*tls.Config, error) {
	files := config.TLS
	if files.CertFile == "" {
		return nil, nil
	}
	pair, err := tls.LoadX509KeyPair(files.CertFile, files.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load management TLS certificate: %w", err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	if files.ClientCAFile != "" {
		pem, err := os.ReadFile(files.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read management client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("management client CA contains no certificates")
		}
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		tlsConfig.ClientCAs = pool
	}
	return tlsConfig, nil
}
