package settings

import (
	"errors"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type WireGuard struct {
	Interface string `yaml:"interface"`
}

func (config WireGuard) Enabled() bool { return config.Interface != "" }

func (config WireGuard) validate(management Management) error {
	if !config.Enabled() {
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
