package settings

import (
	"errors"
	"path/filepath"
	"strings"
)

type Host struct {
	StoragePath string `yaml:"storage_path"`
}

func (host Host) validate() error {
	path := host.StoragePath
	if path != "" && (!filepath.IsAbs(path) || len(path) > 4095 || strings.ContainsRune(path, 0)) {
		return errors.New("host.storage_path must be an absolute Linux path of at most 4095 bytes without NUL")
	}
	return nil
}
