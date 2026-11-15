package settings

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrInvalidConfig identifies invalid configuration without exposing YAML values.
var ErrInvalidConfig = errors.New("invalid configuration")

type Config struct {
	Node           Node           `yaml:"node"`
	Communications Communications `yaml:"communications"`
}

type Node struct {
	ID string `yaml:"id"`
}

type Communications struct {
	GRPC GRPC `yaml:"grpc"`
}

type GRPC struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

// Load reads Node settings without consulting environment variables.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	var config Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		// Native decoder errors can contain credential values as settings expand.
		return Config{}, fmt.Errorf("%w: invalid YAML, fields or value types", ErrInvalidConfig)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: exactly one YAML document is required", ErrInvalidConfig)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

// Validate checks the settings owned by this implemented foundation.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Node.ID) == "" {
		return fmt.Errorf("%w: node.id is required", ErrInvalidConfig)
	}
	if c.Communications.GRPC.Enabled {
		_, port, err := net.SplitHostPort(c.Communications.GRPC.Listen)
		if err != nil {
			return fmt.Errorf("%w: communications.grpc.listen requires host:port", ErrInvalidConfig)
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return fmt.Errorf("%w: communications.grpc.listen requires a port from 0 to 65535", ErrInvalidConfig)
		}
	}
	return nil
}
