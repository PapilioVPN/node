// Package settings loads Node's explicit deployment configuration.
package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
	"go.yaml.in/yaml/v3"
)

type Config struct {
	StartupTimeout  time.Duration  `yaml:"startup_timeout"`
	ShutdownTimeout time.Duration  `yaml:"shutdown_timeout"`
	Communications  Communications `yaml:"communications"`
}

type Communications struct {
	HTTP *HTTP `yaml:"http"`
}

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
	if http := config.Communications.HTTP; http != nil {
		base := filepath.Dir(path)
		http.TLS.Certificate = relativeTo(base, http.TLS.Certificate)
		http.TLS.PrivateKey = relativeTo(base, http.TLS.PrivateKey)
		http.TLS.CA = relativeTo(base, http.TLS.CA)
	}
	return config, nil
}

func (config Config) Validate() error {
	if config.StartupTimeout <= 0 || config.ShutdownTimeout <= 0 {
		return errors.New("startup_timeout and shutdown_timeout must be positive")
	}
	http := config.Communications.HTTP
	if http == nil {
		return nil
	}
	_, port, err := net.SplitHostPort(http.Address)
	if err != nil {
		return fmt.Errorf("communications.http.address: %w", err)
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("communications.http.address requires a numeric TCP port")
	}
	if strings.TrimSpace(http.TLS.Certificate) == "" || strings.TrimSpace(http.TLS.PrivateKey) == "" || strings.TrimSpace(http.TLS.CA) == "" {
		return errors.New("communications.http.tls requires certificate, private_key and ca")
	}
	if http.ReadTimeout <= 0 || http.WriteTimeout <= 0 || http.IdleTimeout <= 0 || http.ShutdownTimeout <= 0 {
		return errors.New("HTTP timeouts must be positive")
	}
	if http.ShutdownTimeout >= config.ShutdownTimeout {
		return errors.New("HTTP shutdown_timeout must fit inside Node shutdown_timeout")
	}
	if http.BodyLimit <= 0 || http.Concurrency <= 0 {
		return errors.New("HTTP body_limit and concurrency must be positive")
	}
	return nil
}

func relativeTo(base, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(base, path)
}
