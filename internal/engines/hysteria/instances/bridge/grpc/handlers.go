// Package grpc exposes Hysteria operations on Node's authenticated gRPC server.
package grpc

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/v1/hysteria_instances.proto

import (
	"context"
	"encoding/hex"

	"github.com/PapilioVPN/node/internal/buildinfo"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
	instances "github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/grpcapi"
	"google.golang.org/grpc"
)

func Register(registrar grpc.ServiceRegistrar, service *instances.Service) {
	v1.RegisterHysteriaInstancesServiceServer(registrar, &instanceHandler{service: service})
}

type instanceHandler struct {
	v1.UnimplementedHysteriaInstancesServiceServer
	service *instances.Service
}

func (h *instanceHandler) GetStatus(ctx context.Context, r *v1.HysteriaInstanceRequest) (*v1.HysteriaInstanceStatus, error) {
	return statusResult(h.service.GetStatus(ctx, r.GetId()))
}
func (h *instanceHandler) Start(ctx context.Context, r *v1.StartHysteriaRequest) (*v1.HysteriaInstanceStatus, error) {
	return statusResult(h.service.Start(ctx, instances.ConfigRequest{ID: r.GetId(), Source: r.GetConfigYaml()}))
}
func (h *instanceHandler) Stop(ctx context.Context, r *v1.HysteriaInstanceRequest) (*v1.HysteriaInstanceStatus, error) {
	return statusResult(h.service.Stop(ctx, r.GetId()))
}
func statusResult(s instances.Status, err error) (*v1.HysteriaInstanceStatus, error) {
	r := &v1.HysteriaInstanceStatus{Id: s.ID, State: v1.HysteriaInstanceState(s.State), HysteriaVersion: s.Version, NodeVersion: buildinfo.Version, StartAction: v1.HysteriaStartAction(s.Action)}
	if s.Digest != nil {
		r.AppliedConfigSha256 = new(hex.EncodeToString(s.Digest[:]))
	}
	if err == nil {
		return r, nil
	}
	st := grpcapi.ErrorStatus(err)
	if s.ID != "" {
		if detail, attachErr := st.WithDetails(r); attachErr == nil {
			st = detail
		}
	}
	return nil, st.Err()
}
