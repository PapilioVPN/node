package settings

import (
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
)

// Instance is an ordered declaration of a Node-owned execution. An empty list
// retains management-only operation. Engine configuration remains a separate file.
type Instance struct {
	ID          string        `yaml:"id"`
	Engine      string        `yaml:"engine"`
	StopTimeout time.Duration `yaml:"stop_timeout"`
	Xray        *Xray         `yaml:"xray"`
}
type Xray struct {
	Binary        string   `yaml:"binary"`
	Configuration string   `yaml:"configuration"`
	API           *XrayAPI `yaml:"api"`
}
type XrayAPI struct {
	Network string `yaml:"network"`
	Address string `yaml:"address"`
}

func validateInstances(instances []Instance) error {
	if len(instances) > 128 {
		return errors.New("at most 128 instances are supported")
	}
	seen := make(map[string]bool, len(instances))
	for i, instance := range instances {
		if validators.ID(instance.ID) != nil || seen[instance.ID] {
			return fmt.Errorf("instances[%d]: invalid or duplicate id", i)
		}
		seen[instance.ID] = true
		if instance.StopTimeout <= 0 {
			return fmt.Errorf("instances[%d]: stop_timeout must be positive", i)
		}
		if instance.Engine != "xray" || instance.Xray == nil {
			return fmt.Errorf("instances[%d]: expected xray engine settings", i)
		}
		for _, path := range []string{instance.Xray.Binary, instance.Xray.Configuration} {
			if path == "" || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
				return fmt.Errorf("instances[%d]: binary and configuration must be UTF-8 paths", i)
			}
		}
		if api := instance.Xray.API; api != nil {
			switch api.Network {
			case "tcp":
				address, err := netip.ParseAddrPort(api.Address)
				if err != nil || !address.Addr().IsLoopback() || address.Port() == 0 || address.Addr().Zone() != "" {
					return fmt.Errorf("instances[%d]: API must use numeric loopback TCP", i)
				}
			case "unix":
				if api.Address == "" || strings.ContainsRune(api.Address, 0) || !utf8.ValidString(api.Address) || len(api.Address) > 107 {
					return fmt.Errorf("instances[%d]: invalid Unix API path", i)
				}
			default:
				return fmt.Errorf("instances[%d]: unsupported API network", i)
			}
		}
	}
	return nil
}
func resolveInstances(base string, instances []Instance) {
	for i := range instances {
		instance := &instances[i]
		instance.Xray.Binary = absolutePath(base, instance.Xray.Binary)
		instance.Xray.Configuration = absolutePath(base, instance.Xray.Configuration)
		if api := instance.Xray.API; api != nil && api.Network == "unix" {
			api.Address = absolutePath(base, api.Address)
		}
	}
}
func absolutePath(base, value string) string {
	if filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(base, value)
}
