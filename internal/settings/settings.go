// Package settings loads explicit settings for a Node process.
package settings

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
)

const maxBytes = 64 << 10

type Config struct {
	Management Management `yaml:"management"`
	Host       Host       `yaml:"host"`
	Xray       Xray       `yaml:"xray"`
	WireGuard  WireGuard  `yaml:"wireguard"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return Config{}, err
	}
	if len(data) > maxBytes {
		return Config{}, errors.New("settings exceed 64 KiB")
	}

	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		// The YAML decoder can include untrusted scalar values in an error.
		return Config{}, errors.New("settings must contain one valid YAML document with known fields")
	}
	if err := decoder.Decode(new(yaml.Node)); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("settings must contain exactly one YAML document")
	}
	dir := filepath.Dir(path)
	if err := config.Management.validate(dir); err != nil {
		return Config{}, err
	}
	if err := config.Host.validate(); err != nil {
		return Config{}, err
	}
	if err := config.Xray.validate(dir, config.Management); err != nil {
		return Config{}, err
	}
	if err := config.WireGuard.validate(config.Management); err != nil {
		return Config{}, err
	}
	return config, nil
}

func resolve(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
