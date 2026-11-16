// Package grpcapi adapts Xray outbound observations to their wire contract.
package grpcapi

type OutboundAPI struct {
	Outbounds          OutboundStatusReader
	AllowOutboundsRead bool
}
