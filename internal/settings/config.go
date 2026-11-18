// Package settings loads Node's explicit deployment configuration.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"go.yaml.in/yaml/v3"
)

type Config struct {
	StartupTimeout  time.Duration  `yaml:"startup_timeout"`
	ShutdownTimeout time.Duration  `yaml:"shutdown_timeout"`
	Communications  Communications `yaml:"communications"`
	System          System         `yaml:"system"`
}

type Communications struct {
	HTTP *HTTP `yaml:"http"`
	GRPC *GRPC `yaml:"grpc"`
	NATS *NATS `yaml:"nats"`
}

// Load accepts one strictly typed YAML document. Relative identity paths are
// resolved against the configuration file, independently of the working directory.
// It neither reads environment overrides nor creates deployment resources.
func Load(path string) (Config, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Config{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	const maxSize = 1 << 20
	contents, err := io.ReadAll(io.LimitReader(file, maxSize+1))
	if err != nil {
		return Config{}, err
	}
	if len(contents) > maxSize {
		return Config{}, errors.New("configuration exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode configuration: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err == nil {
		return Config{}, errors.New("configuration must contain exactly one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode configuration trailer: %w", err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	base := filepath.Dir(path)
	if http := config.Communications.HTTP; http != nil {
		http.TLS = resolveTLS(base, http.TLS)
	}
	if grpc := config.Communications.GRPC; grpc != nil {
		grpc.TLS = resolveTLS(base, grpc.TLS)
	}
	if nats := config.Communications.NATS; nats != nil {
		nats.TLS = resolveTLS(base, nats.TLS)
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.StartupTimeout <= 0 || config.ShutdownTimeout <= 0 {
		return errors.New("startup_timeout and shutdown_timeout must be positive")
	}
	remaining := config.ShutdownTimeout
	if http := config.Communications.HTTP; http != nil {
		if err := http.validate(); err != nil {
			return fmt.Errorf("communications.http: %w", err)
		}
		if http.ShutdownTimeout >= remaining {
			return errors.New("transport shutdown budgets must fit inside Node shutdown_timeout")
		}
		remaining -= http.ShutdownTimeout
	}
	if grpc := config.Communications.GRPC; grpc != nil {
		if err := grpc.validate(); err != nil {
			return fmt.Errorf("communications.grpc: %w", err)
		}
		if grpc.ShutdownTimeout >= remaining {
			return errors.New("transport shutdown budgets must fit inside Node shutdown_timeout")
		}
		remaining -= grpc.ShutdownTimeout
	}
	if nats := config.Communications.NATS; nats != nil {
		if err := nats.validate(); err != nil {
			return fmt.Errorf("communications.nats: %w", err)
		}
		if nats.ShutdownTimeout >= remaining {
			return errors.New("transport shutdown budgets must fit inside Node shutdown_timeout")
		}
	}
	return config.validateSystem(remaining)
}
