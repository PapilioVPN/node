// Package api observes read-only native API reachability and registrations.
package api

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

type Reader struct {
	stats      stats.StatsServiceClient
	reflection reflection.ServerReflectionClient
}

func New(conn grpc.ClientConnInterface) Reader {
	return Reader{stats: stats.NewStatsServiceClient(conn), reflection: reflection.NewServerReflectionClient(conn)}
}

var services = []struct{ name, symbol string }{
	{"handlers", "xray.app.proxyman.command.HandlerService"},
	{"logger", "xray.app.log.command.LoggerService"},
	{"observatory", "xray.core.app.observatory.command.ObservatoryService"},
	{"routing", "xray.app.router.command.RoutingService"},
	{"stats", "xray.app.stats.command.StatsService"},
}

func (r Reader) Read(ctx context.Context) (domain.Native, error) {
	if err := ctx.Err(); err != nil {
		return domain.Native{}, err
	}
	n := domain.Native{Capabilities: make([]domain.Capability, 0, len(services))}
	probe, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	result, err := r.stats.GetSysStats(probe, &stats.SysStatsRequest{}, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	cancel()
	if err == nil && result == nil {
		err = domain.ErrMalformed
	}
	n.Runtime = evidence("xray.stats.get_sys_stats", err)
	if err == nil {
		value := uint64(result.Uptime)
		n.UptimeSeconds = &value
	}
	if err := ctx.Err(); err != nil {
		return domain.Native{}, err
	}
	discover, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	advertised, reflectionErr := r.registrations(discover)
	cancel()
	n.Discovery = evidence("grpc.reflection.v1.list_services", reflectionErr)
	for _, service := range services {
		c := domain.Capability{Name: service.name, State: "unknown", Basis: "not_verified"}
		if reflectionErr == nil {
			c.State = "not_registered"
			c.Basis = "native_reflection"
			if advertised[service.symbol] {
				c.State = "advertised"
			}
		}
		if service.name == "stats" && err == nil {
			c.State = "verified"
			c.Basis = "get_sys_stats_only"
		}
		n.Capabilities = append(n.Capabilities, c)
	}
	switch {
	case err == nil || reflectionErr == nil || status.Code(err) == codes.Unimplemented || status.Code(reflectionErr) == codes.Unimplemented:
		n.Evidence = evidence("xray.native_api", nil)
	default:
		n.Evidence = evidence("xray.native_api", errors.Join(err, reflectionErr))
	}
	if err := ctx.Err(); err != nil {
		return domain.Native{}, err
	}
	return n, nil
}
func (r Reader) registrations(ctx context.Context) (map[string]bool, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := r.reflection.ServerReflectionInfo(streamCtx, grpc.MaxCallRecvMsgSize(domain.MaxNativeBytes))
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	if err := stream.Send(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_ListServices{ListServices: ""}}); err != nil {
		return nil, err
	}
	response, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	if e := response.GetErrorResponse(); e != nil {
		if e.ErrorCode == 0 {
			return nil, domain.ErrMalformed
		}
		return nil, status.Error(codes.Code(e.ErrorCode), "native reflection rejected")
	}
	list := response.GetListServicesResponse()
	if list == nil {
		return nil, domain.ErrMalformed
	}
	if len(list.Service) > domain.MaxServices {
		return nil, domain.ErrCapacity
	}
	found := make(map[string]bool, len(list.Service))
	for _, s := range list.Service {
		if s == nil || len(s.Name) > 256 || s.Name == "" || found[s.Name] {
			return nil, domain.ErrMalformed
		}
		found[s.Name] = true
	}
	return found, nil
}
func evidence(source string, err error) domain.Evidence {
	at := time.Now().UTC()
	e := domain.Evidence{State: "available", Source: source, ObservedAt: &at, Cause: err}
	if err == nil {
		return e
	}
	e.State = "unavailable"
	e.Reason = "source_unavailable"
	switch {
	case errors.Is(err, domain.ErrMalformed):
		e.Reason = "invalid_response"
	case errors.Is(err, domain.ErrCapacity), status.Code(err) == codes.ResourceExhausted:
		e.Reason = "resource_exhausted"
	case errors.Is(err, rpc.ErrClosed):
		e.Reason = "closed"
	case errors.Is(err, context.DeadlineExceeded), status.Code(err) == codes.DeadlineExceeded:
		e.Reason = "deadline_exceeded"
	case errors.Is(err, context.Canceled), status.Code(err) == codes.Canceled:
		e.Reason = "canceled"
	case status.Code(err) == codes.Unimplemented:
		e.State = "unsupported"
		e.Reason = "method_not_registered"
	case status.Code(err) == codes.PermissionDenied, status.Code(err) == codes.Unauthenticated:
		e.Reason = "native_permission_denied"
	}
	return e
}
