package settings

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// Instance declares one named engine execution started with Node.
type Instance struct {
	ID         domain.InstanceID `yaml:"id"`
	Engine     domain.EngineKind `yaml:"engine"`
	Xray       *Xray             `yaml:"xray,omitempty"`
	Validation *ConfigValidation `yaml:"validation,omitempty"`
}

// ConfigValidation enables candidate building with explicit per-instance limits.
// WorkDir is an existing writable directory; loading settings never creates it.
type ConfigValidation struct {
	WorkDir       string        `yaml:"work_dir"`
	Timeout       time.Duration `yaml:"timeout"`
	MaxBytes      int           `yaml:"max_bytes"`
	MaxConcurrent int           `yaml:"max_concurrent"`
}

// Xray contains native execution settings; Xray itself parses ConfigFile.
type Xray struct {
	Binary      string            `yaml:"binary"`
	ConfigFile  string            `yaml:"config_file"`
	StopTimeout time.Duration     `yaml:"stop_timeout"`
	Inspection  *BinaryInspection `yaml:"inspection,omitempty"`
	API         *XrayAPI          `yaml:"api,omitempty"`
}

// XrayAPI connects to an explicitly provisioned local native API endpoint.
// TCP addresses are numeric loopback; Unix addresses are filesystem paths.
type XrayAPI struct {
	Network         string        `yaml:"network"`
	Address         string        `yaml:"address"`
	RequestTimeout  time.Duration `yaml:"request_timeout"`
	MaxReceiveBytes int           `yaml:"max_receive_bytes"`
	MaxConcurrent   int           `yaml:"max_concurrent"`
}

// BinaryInspection enables bounded executable version reads without runtime control.
type BinaryInspection struct {
	Timeout        time.Duration `yaml:"timeout"`
	MaxOutputBytes int           `yaml:"max_output_bytes"`
	MaxConcurrent  int           `yaml:"max_concurrent"`
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
		if inspection := xray.Inspection; inspection != nil {
			if inspection.Timeout <= 0 || inspection.MaxOutputBytes <= 0 || inspection.MaxConcurrent <= 0 {
				return fmt.Errorf("%w: instances[%d].xray.inspection requires positive timeout, max_output_bytes and max_concurrent", ErrInvalidConfig, i)
			}
		}
		if api := xray.API; api != nil {
			if api.RequestTimeout <= 0 || api.MaxReceiveBytes <= 0 || api.MaxConcurrent <= 0 {
				return fmt.Errorf("%w: instances[%d].xray.api requires positive request_timeout, max_receive_bytes and max_concurrent", ErrInvalidConfig, i)
			}
			switch api.Network {
			case "unix":
				if strings.TrimSpace(api.Address) == "" || strings.ContainsRune(api.Address, 0) {
					return fmt.Errorf("%w: instances[%d].xray.api requires a Unix socket path", ErrInvalidConfig, i)
				}
			case "tcp":
				address, err := netip.ParseAddrPort(api.Address)
				if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 {
					return fmt.Errorf("%w: instances[%d].xray.api requires a numeric loopback address and positive port", ErrInvalidConfig, i)
				}
			default:
				return fmt.Errorf("%w: instances[%d].xray.api requires unix or tcp network", ErrInvalidConfig, i)
			}
		}
		if validation := instance.Validation; validation != nil {
			if strings.TrimSpace(validation.WorkDir) == "" || validation.Timeout <= 0 || validation.MaxBytes <= 0 || validation.MaxConcurrent <= 0 {
				return fmt.Errorf("%w: instances[%d].validation requires work_dir and positive timeout, max_bytes and max_concurrent", ErrInvalidConfig, i)
			}
		}
	}
	return nil
}
