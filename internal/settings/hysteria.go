package settings

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type Hysteria struct {
	Instances []HysteriaInstance `yaml:"instances"`
}

type HysteriaInstance struct {
	ID               string `yaml:"id"`
	Executable       string `yaml:"executable"`
	ConfigFile       string `yaml:"config_file"`
	RuntimeDirectory string `yaml:"runtime_directory"`
}

func (config *Hysteria) validate(dir string, management Management) error {
	if len(config.Instances) == 0 {
		return nil
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote hysteria management requires TLS with an API key or a client CA")
	}
	ids := make(map[string]struct{}, len(config.Instances))
	for i := range config.Instances {
		instance := &config.Instances[i]
		if !validInstanceID(instance.ID) {
			return fmt.Errorf("hysteria.instances[%d].id must be 1 to 63 lowercase ASCII letters, digits or hyphens, starting and ending with a letter or digit", i)
		}
		if _, exists := ids[instance.ID]; exists {
			return fmt.Errorf("hysteria.instances[%d].id is a duplicate", i)
		}
		ids[instance.ID] = struct{}{}

		for _, field := range []struct {
			name string
			path *string
		}{
			{"executable", &instance.Executable},
			{"config_file", &instance.ConfigFile},
			{"runtime_directory", &instance.RuntimeDirectory},
		} {
			if strings.TrimSpace(*field.path) == "" {
				return fmt.Errorf("hysteria.instances[%d].%s is required", i, field.name)
			}
			if strings.ContainsRune(*field.path, 0) {
				return fmt.Errorf("hysteria.instances[%d].%s must not contain NUL", i, field.name)
			}
			absolute, err := filepath.Abs(resolve(dir, *field.path))
			if err != nil {
				return fmt.Errorf("resolve hysteria.instances[%d].%s: %w", i, field.name, err)
			}
			*field.path = absolute
		}
	}
	return nil
}
