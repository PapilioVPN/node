package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Xray struct {
	ID               string `yaml:"id"`
	Executable       string `yaml:"executable"`
	ConfigFile       string `yaml:"config_file"`
	RuntimeDirectory string `yaml:"runtime_directory"`
}

func (xray Xray) Enabled() bool { return xray.ID != "" }

func (xray *Xray) validate(dir string, management Management) error {
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
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote xray management requires TLS with an API key or a client CA")
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
