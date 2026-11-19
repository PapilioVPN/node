package settings

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Sessions identifies host observation sources only. It does not configure API
// permissions or per-operation timeouts. Missing sources do not prevent startup.
type Sessions struct {
	BusSocketPath string `yaml:"bus_socket_path"`
	UtmpPath      string `yaml:"utmp_path"`
}

func (config Sessions) Defaults() Sessions {
	if config.BusSocketPath == "" {
		config.BusSocketPath = "/run/dbus/system_bus_socket"
	}
	if config.UtmpPath == "" {
		config.UtmpPath = "/run/utmp"
	}
	return config
}
func (config Sessions) validate() error {
	config = config.Defaults()
	if !filepath.IsAbs(config.BusSocketPath) || len(config.BusSocketPath) > 107 || strings.ContainsRune(config.BusSocketPath, 0) || !utf8.ValidString(config.BusSocketPath) {
		return errors.New("sessions.bus_socket_path must be an absolute UTF-8 Unix socket path of at most 107 bytes")
	}
	if !filepath.IsAbs(config.UtmpPath) || len(config.UtmpPath) > 4095 || strings.ContainsRune(config.UtmpPath, 0) || !utf8.ValidString(config.UtmpPath) {
		return errors.New("sessions.utmp_path must be an absolute UTF-8 path of at most 4095 bytes")
	}
	return nil
}
