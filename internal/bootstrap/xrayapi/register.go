package xrayapi

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	onlinegrpc "github.com/PapilioVPN/node/internal/modules/xray/online/grpcapi"
	outboundgrpc "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type Services struct {
	fx.In
	Registrar grpc.ServiceRegistrar
	Info      infogrpc.InfoAPI
	Outbounds outboundgrpc.OutboundAPI
	Users     usergrpc.UserAPI
	Online    onlinegrpc.OnlineAPI
	Routing   routinggrpc.RoutingAPI
	Traffic   trafficgrpc.TrafficAPI
}

// Register preserves the single existing service while capabilities own methods.
func Register(p Services) {
	xrayv1.RegisterXrayServiceServer(p.Registrar, Handler{InfoAPI: p.Info, OutboundAPI: p.Outbounds, UserAPI: p.Users, OnlineAPI: p.Online, RoutingAPI: p.Routing, TrafficAPI: p.Traffic})
}
