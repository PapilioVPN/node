package settings

import (
	"fmt"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// Instance declares one named engine execution started with Node.
type Instance struct {
	ID     domain.InstanceID `yaml:"id"`
	Engine domain.EngineKind `yaml:"engine"`
	Xray   *Xray             `yaml:"xray,omitempty"`
}

// Xray contains process settings; Xray itself parses ConfigFile.
type Xray struct {
	Binary      string        `yaml:"binary"`
	ConfigFile  string        `yaml:"config_file"`
	StopTimeout time.Duration `yaml:"stop_timeout"`
}

func validateInstances(instances []Instance) error {
	seen := make(map[domain.InstanceID]struct{}, len(instances))
	for i, instance := range instances {
		if strings.TrimSpace(string(instance.ID)) == "" {
			return fmt.Errorf("%w: instances[%d].id is required", ErrInvalidConfig, i)
		}
		if _, exists := seen[instance.ID]; exists {
			return fmt.Errorf("%w: instances[%d].id must be unique", ErrInvalidConfig, i)
		}
		seen[instance.ID] = struct{}{}
		if instance.Engine != domain.Xray || instance.Xray == nil {
			return fmt.Errorf("%w: instances[%d] requires a supported engine and its settings", ErrInvalidConfig, i)
		}
		xray := instance.Xray
		if strings.TrimSpace(xray.Binary) == "" || strings.TrimSpace(xray.ConfigFile) == "" || xray.StopTimeout <= 0 {
			return fmt.Errorf("%w: instances[%d].xray requires binary, config_file and a positive stop_timeout", ErrInvalidConfig, i)
		}
	}
	return nil
}
