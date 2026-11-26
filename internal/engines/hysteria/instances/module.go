// Package instances composes Hysteria's process and lifecycle capability.
package instances

import (
	"errors"
	"path/filepath"
	"strings"

	grpcapi "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc"
	httpapi "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/http"
	"github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/hysteria/instances/process"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

type Config struct {
	ID, Executable, ConfigFile, RuntimeDirectory string
}

type Capability struct{ *lifecycle.Service }

func New(configs []Config) (Capability, error) {
	backends := make(map[string]lifecycle.Backend, len(configs))
	for _, c := range configs {
		if !validID(c.ID) || !validPath(c.Executable) || !validPath(c.ConfigFile) || !validPath(c.RuntimeDirectory) || backends[c.ID] != nil {
			return Capability{}, errors.New("invalid Hysteria instance settings")
		}
		backends[c.ID] = process.Backend{Executable: c.Executable, ConfigFile: c.ConfigFile, RuntimeDirectory: c.RuntimeDirectory}
	}
	return Capability{lifecycle.New(backends)}, nil
}

func validPath(path string) bool { return filepath.IsAbs(path) && !strings.ContainsRune(path, 0) }

func validID(id string) bool {
	if len(id) == 0 || len(id) > 63 || id[0] == '-' || id[len(id)-1] == '-' {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func (c Capability) RegisterHTTP(router chi.Router) { httpapi.Register(router, c.Service) }
func (c Capability) RegisterGRPC(registrar grpc.ServiceRegistrar) {
	grpcapi.Register(registrar, c.Service)
}
