// Package xrayapi composes the existing XrayService without owning workflows.
package xrayapi

import (
	outboundgrpc "github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/grpcapi"
	routinggrpc "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi"
	trafficgrpc "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi"
	usergrpc "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	onlinegrpc "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi"
)

type Handler struct {
	usergrpc.UserAPI
	onlinegrpc.OnlineAPI
	routinggrpc.RoutingAPI
	trafficgrpc.TrafficAPI
	infogrpc.InfoAPI
	outboundgrpc.OutboundAPI
}
