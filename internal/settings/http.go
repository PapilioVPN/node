package settings

import (
	"fmt"
	"strings"
	"time"
)

// HTTP bounds the mutually authenticated management listener.
type HTTP struct {
	Enabled        bool          `yaml:"enabled"`
	Listen         string        `yaml:"listen"`
	RequestTimeout time.Duration `yaml:"request_timeout"`
	ReadTimeout    time.Duration `yaml:"read_timeout"`
	WriteTimeout   time.Duration `yaml:"write_timeout"`
	IdleTimeout    time.Duration `yaml:"idle_timeout"`
	MaxBodyBytes   int           `yaml:"max_body_bytes"`
	MaxHeaderBytes int           `yaml:"max_header_bytes"`
	MaxConcurrent  int           `yaml:"max_concurrent"`
	MaxConnections int           `yaml:"max_connections"`
	TLS            TLS           `yaml:"tls"`
}

func (c HTTP) validate() error {
	if !c.Enabled {
		return nil
	}
	if !validListen(c.Listen) {
		return fmt.Errorf("%w: communications.http.listen requires host:port", ErrInvalidConfig)
	}
	if c.RequestTimeout <= 0 || c.ReadTimeout <= 0 || c.WriteTimeout <= c.RequestTimeout || c.IdleTimeout <= 0 {
		return fmt.Errorf("%w: positive HTTP timeouts and write_timeout greater than request_timeout are required", ErrInvalidConfig)
	}
	if c.MaxBodyBytes <= 0 || c.MaxHeaderBytes <= 0 || c.MaxConcurrent <= 0 || c.MaxConnections <= 0 {
		return fmt.Errorf("%w: positive HTTP body/header/concurrency limits are required", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.TLS.CertificateFile) == "" || strings.TrimSpace(c.TLS.PrivateKeyFile) == "" || strings.TrimSpace(c.TLS.ClientCAFile) == "" {
		return fmt.Errorf("%w: HTTP server certificate, private key and client CA files are required", ErrInvalidConfig)
	}
	return nil
}
