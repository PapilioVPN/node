package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type OpenVPN struct {
	ID                 string `yaml:"id"`
	Executable         string `yaml:"executable"`
	ConfigFile         string `yaml:"config_file"`
	RuntimeDirectory   string `yaml:"runtime_directory"`
	RevocationCAFile   string `yaml:"revocation_ca_file"`
	RevocationListFile string `yaml:"revocation_list_file"`
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
	if (config.RevocationCAFile == "") != (config.RevocationListFile == "") {
		return errors.New("openvpn requires both revocation_ca_file and revocation_list_file")
	}
	for _, path := range []*string{&config.Executable, &config.ConfigFile, &config.RuntimeDirectory, &config.RevocationCAFile, &config.RevocationListFile} {
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
	if config.RevocationCAFile != "" && config.RevocationCAFile == config.RevocationListFile {
		return errors.New("openvpn issuer and revocation list paths must be different")
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote openvpn management requires TLS with an API key or a client CA")
	}
	return nil
}
