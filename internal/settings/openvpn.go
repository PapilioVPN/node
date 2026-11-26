package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type OpenVPN struct {
	ID               string `yaml:"id"`
	Executable       string `yaml:"executable"`
	ConfigFile       string `yaml:"config_file"`
	RuntimeDirectory string `yaml:"runtime_directory"`
}

func (config OpenVPN) Enabled() bool { return config.ID != "" }

func (config *OpenVPN) validate(dir string, management Management) error {
	if *config == (OpenVPN{}) {
		return nil
	}
	if !validInstanceID(config.ID) {
		return errors.New("openvpn.id must be 1 to 63 lowercase ASCII letters, digits or hyphens, starting and ending with a letter or digit")
	}
	if config.Executable == "" || config.ConfigFile == "" {
		return errors.New("openvpn requires executable and config_file")
	}
	for _, path := range []*string{&config.Executable, &config.ConfigFile, &config.RuntimeDirectory} {
		if *path == "" {
			continue
		}
		if strings.ContainsRune(*path, 0) {
			return errors.New("openvpn paths must not contain NUL")
		}
		resolved, err := filepath.Abs(resolve(dir, *path))
		if err != nil {
			return fmt.Errorf("resolve openvpn path: %w", err)
		}
		*path = resolved
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote openvpn management requires TLS with an API key or a client CA")
	}
	return nil
}
