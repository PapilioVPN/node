package settings

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type WireGuard struct {
	Interface      string          `yaml:"interface"`
	Managed        bool            `yaml:"managed"`
	StateDirectory string          `yaml:"state_directory"`
	Probe          *WireGuardProbe `yaml:"probe"`
}

type WireGuardProbe struct {
	URL                 string `yaml:"url"`
	Interface           string `yaml:"interface"`
	TimeoutMilliseconds uint32 `yaml:"timeout_ms"`
}

func (config WireGuard) Enabled() bool { return config.Interface != "" }

func (config *WireGuard) validate(dir string, management Management) error {
	if probe := config.Probe; probe != nil {
		if !config.Managed {
			return errors.New("wireguard.probe requires managed mode")
		}
		target, err := url.Parse(probe.URL)
		if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Fragment != "" {
			return errors.New("wireguard.probe.url must be an absolute HTTP or HTTPS URL without credentials or a fragment")
		}
		if probe.Interface == "" {
			probe.Interface = config.Interface
		}
		if !validWireGuardInterface(probe.Interface) {
			return errors.New("wireguard.probe.interface must be a valid Linux interface name")
		}
		if probe.TimeoutMilliseconds == 0 {
			probe.TimeoutMilliseconds = 1500
		}
		if probe.TimeoutMilliseconds > 5000 {
			return errors.New("wireguard.probe.timeout_ms exceeds 5000")
		}
	}
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
	if !validWireGuardInterface(name) {
		return errors.New("wireguard.interface must be a valid Linux interface name")
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote wireguard management requires TLS with an API key or a client CA")
	}
	return nil
}

func validWireGuardInterface(name string) bool {
	return name != "" && len(name) < unix.IFNAMSIZ && utf8.ValidString(name) && name != "." && name != ".." &&
		!strings.ContainsAny(name, "\x00/: \t\n\v\f\r")
}
