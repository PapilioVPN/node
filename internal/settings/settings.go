// Package settings loads explicit settings for a Node process.
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

	"go.yaml.in/yaml/v3"
)

const maxBytes = 64 << 10

type Config struct {
	Management Management `yaml:"management"`
	Host       Host       `yaml:"host"`
	Xray       Xray       `yaml:"xray"`
}

type Xray struct {
	ID               string `yaml:"id"`
	Executable       string `yaml:"executable"`
	ConfigFile       string `yaml:"config_file"`
	RuntimeDirectory string `yaml:"runtime_directory"`
}

func (config Xray) Enabled() bool { return config.ID != "" }

type Host struct {
	StoragePath string `yaml:"storage_path"`
}

type Management struct {
	HTTPAddress string `yaml:"http_address"`
	GRPCAddress string `yaml:"grpc_address"`
	APIKey      string `yaml:"api_key"`
	TLS         TLS    `yaml:"tls"`
}

type TLS struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	ClientCAFile string `yaml:"client_ca_file"`
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
	management := &config.Management
	if err := validateAddress(management.HTTPAddress); err != nil {
		return Config{}, fmt.Errorf("management.http_address: %w", err)
	}
	if err := validateAddress(management.GRPCAddress); err != nil {
		return Config{}, fmt.Errorf("management.grpc_address: %w", err)
	}

	tls := &management.TLS
	if (tls.CertFile == "") != (tls.KeyFile == "") {
		return Config{}, errors.New("management.tls requires both cert_file and key_file")
	}
	if tls.CertFile == "" && (management.APIKey != "" || tls.ClientCAFile != "") {
		return Config{}, errors.New("management credentials require TLS")
	}
	dir := filepath.Dir(path)
	tls.CertFile = resolve(dir, tls.CertFile)
	tls.KeyFile = resolve(dir, tls.KeyFile)
	tls.ClientCAFile = resolve(dir, tls.ClientCAFile)
	storagePath := config.Host.StoragePath
	if storagePath != "" && (!filepath.IsAbs(storagePath) || len(storagePath) > 4095 || strings.ContainsRune(storagePath, 0)) {
		return Config{}, errors.New("host.storage_path must be an absolute Linux path of at most 4095 bytes without NUL")
	}
	if err := config.validateXray(dir); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) validateXray(dir string) error {
	xray := &config.Xray
	if *xray == (Xray{}) {
		return nil
	}
	if !validInstanceID(xray.ID) {
		return errors.New("xray.id must be 1 to 63 lowercase ASCII letters, digits or hyphens, starting and ending with a letter or digit")
	}
	if xray.Executable == "" || xray.ConfigFile == "" {
		return errors.New("xray requires executable and config_file")
	}
	for _, path := range []*string{&xray.Executable, &xray.ConfigFile, &xray.RuntimeDirectory} {
		if *path == "" {
			continue
		}
		if strings.ContainsRune(*path, 0) {
			return errors.New("xray paths must not contain NUL")
		}
		absolute, err := filepath.Abs(resolve(dir, *path))
		if err != nil {
			return fmt.Errorf("resolve xray path: %w", err)
		}
		*path = absolute
	}
	management := config.Management
	if management.APIKey == "" && management.TLS.ClientCAFile == "" {
		for _, address := range []string{management.HTTPAddress, management.GRPCAddress} {
			host, _, _ := net.SplitHostPort(address)
			if !net.ParseIP(host).IsLoopback() {
				return errors.New("remote xray management requires TLS with an API key or a client CA")
			}
		}
	}
	return nil
}

func validInstanceID(id string) bool {
	if id == "" || len(id) > 63 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func validateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("expected a host and port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("expected a numeric port from 0 to 65535")
	}
	return nil
}

func resolve(dir, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(dir, path)
}
