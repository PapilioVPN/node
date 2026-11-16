// Package xrayapi composes the existing XrayService without owning workflows.
package xrayapi

import (
	infogrpc "github.com/PapilioVPN/node/internal/modules/xray/info/grpcapi"
	outboundgrpc "github.com/PapilioVPN/node/internal/modules/xray/outbounds/grpcapi"
	routinggrpc "github.com/PapilioVPN/node/internal/modules/xray/routing/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/modules/xray/traffic/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/modules/xray/users/grpcapi"
)

type Handler struct {
	usergrpc.UserAPI
	routinggrpc.RoutingAPI
	trafficgrpc.TrafficAPI
	infogrpc.InfoAPI
	outboundgrpc.OutboundAPI
}
