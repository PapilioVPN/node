package settings

import (
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
)

type HTTP struct {
	Address         string        `yaml:"address"`
	TLS             mtls.Files    `yaml:"tls"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	IdleTimeout     time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	BodyLimit       int           `yaml:"body_limit"`
	Concurrency     int           `yaml:"concurrency"`
}

func (config HTTP) validate() error {
	if err := validateAddress(config.Address); err != nil {
		return err
	}
	if err := validateTLS(config.TLS); err != nil {
		return err
	}
	if config.ReadTimeout <= 0 || config.WriteTimeout <= 0 || config.IdleTimeout <= 0 || config.ShutdownTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	if config.BodyLimit <= 0 || config.Concurrency <= 0 {
		return errors.New("body_limit and concurrency must be positive")
	}
	return nil
}
