package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type WireGuard struct {
	Interface      string `yaml:"interface"`
	Managed        bool   `yaml:"managed"`
	StateDirectory string `yaml:"state_directory"`
}

func (config WireGuard) Enabled() bool { return config.Interface != "" }

func (config *WireGuard) validate(dir string, management Management) error {
	if config.StateDirectory != "" {
		if !config.Managed {
			return errors.New("wireguard.state_directory requires managed mode")
		}
		if strings.ContainsRune(config.StateDirectory, 0) {
			return errors.New("wireguard.state_directory must not contain NUL")
		}
		path, err := filepath.Abs(resolve(dir, config.StateDirectory))
		if err != nil {
			return fmt.Errorf("resolve wireguard state directory: %w", err)
		}
		config.StateDirectory = path
	}
	if !config.Enabled() {
		if config.Managed {
			return errors.New("wireguard.interface is required when managed")
		}
		return nil
	}
	name := config.Interface
	if len(name) >= unix.IFNAMSIZ || !utf8.ValidString(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, "\x00/: \t\n\v\f\r") {
		return errors.New("wireguard.interface must be a valid Linux interface name")
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote wireguard management requires TLS with an API key or a client CA")
	}
	return nil
}
