// Package grpcapi composes the existing XrayService without owning workflows.
package grpcapi

import (
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type Handler struct {
	usergrpc.UserAPI
	routinggrpc.RoutingAPI
	trafficgrpc.TrafficAPI
	infogrpc.InfoAPI
	Online             OnlineReader
	Outbounds          OutboundStatusReader
	AllowOnlineRead    bool
	AllowOutboundsRead bool
}
