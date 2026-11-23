// Package engine registers the exact traffic reset on the bundled Xray process.
package engine

import (
	"context"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native/scopeapi"
	"github.com/PapilioVPN/node/internal/engines/xray/stats/traffic"
	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/stats"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	common.Must(common.RegisterConfig((*scopeapi.Config)(nil), func(ctx context.Context, _ any) (any, error) {
		service := new(exactTraffic)
		err := core.RequireFeatures(ctx, func(manager stats.Manager) {
			service.manager = manager
		})
		return service, err
	}))
}

// EnableScopeReset adds the private service to the listener owned by Node.
func EnableScopeReset(config *core.Config) error {
	for i, app := range config.GetApp() {
		if app.GetType() != "xray.app.commander.Config" {
			continue
		}
		instance, err := app.GetInstance()
		if err != nil {
			return err
		}
		api := instance.(*commander.Config)
		if api.GetTag() == "papiliovpn-node-api" && filepath.IsAbs(api.GetListen()) {
			api.Service = append(api.Service, serial.ToTypedMessage(new(scopeapi.Config)))
			config.App[i] = serial.ToTypedMessage(api)
		}
	}
	return nil
}

type exactTraffic struct {
	scopeapi.UnimplementedExactTrafficServer
	manager stats.Manager
}

func (service *exactTraffic) Register(server *grpc.Server) {
	scopeapi.RegisterExactTrafficServer(server, service)
}

func (service *exactTraffic) ResetPair(ctx context.Context, request *scopeapi.PairResetRequest) (*scopeapi.PairResetResult, error) {
	scope, name := traffic.Scope(request.GetScope()), request.GetName()
	if !scope.Valid() || name == "" || len(name) > traffic.MaxCounterNameBytes || !utf8.ValidString(name) {
		return nil, status.Error(codes.InvalidArgument, "invalid traffic pair selector")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	prefix := string(scope) + ">>>" + name + ">>>traffic>>>"
	result := &scopeapi.PairResetResult{}
	if counter := service.manager.GetCounter(prefix + "uplink"); counter != nil {
		previous := counter.Set(0)
		result.PreviousUplink = &previous
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if counter := service.manager.GetCounter(prefix + "downlink"); counter != nil {
		previous := counter.Set(0)
		result.PreviousDownlink = &previous
	}
	return result, nil
}

func (service *exactTraffic) ResetScope(ctx context.Context, request *scopeapi.ScopeResetRequest) (*scopeapi.ScopeResetResult, error) {
	scope, limit := traffic.Scope(request.GetScope()), int(request.GetMaxCounters())
	if !scope.Valid() || limit < 1 || limit > traffic.MaxScopeResetCounters {
		return nil, status.Error(codes.InvalidArgument, "invalid traffic scope or limit")
	}
	prefix := string(scope) + ">>>"
	type selectedCounter struct {
		name     string
		counter  stats.Counter
		downlink bool
	}
	selected := make([]selectedCounter, 0, min(limit, 32))
	var nameBytes int
	var exceeded bool
	service.manager.VisitCounters(func(key string, counter stats.Counter) bool {
		if ctx.Err() != nil {
			return false
		}
		key, matches := strings.CutPrefix(key, prefix)
		if !matches {
			return true
		}
		name, direction := traffic.SplitCounter(key)
		if name == "" {
			return true
		}
		nameBytes += len(name)
		exceeded = len(selected) == limit || len(name) > traffic.MaxCounterNameBytes ||
			nameBytes > traffic.MaxScopeResetNameBytes || !utf8.ValidString(name)
		if exceeded {
			return false
		}
		selected = append(selected, selectedCounter{name: name, counter: counter, downlink: direction == traffic.Downlink})
		return true
	})
	if exceeded {
		return &scopeapi.ScopeResetResult{LimitExceeded: true}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}

	result := &scopeapi.ScopeResetResult{Counters: make([]*scopeapi.Counter, len(selected))}
	rows := make([]scopeapi.Counter, len(selected))
	for i, item := range selected {
		rows[i] = scopeapi.Counter{Name: item.name, Downlink: item.downlink, PreviousBytes: item.counter.Set(0)}
		result.Counters[i] = &rows[i]
	}
	return result, nil
}
