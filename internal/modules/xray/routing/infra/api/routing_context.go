package api

import (
	"net/netip"
	"unicode/utf8"

	native "github.com/xtls/xray-core/app/router/command"
	netnative "github.com/xtls/xray-core/common/net"

	"github.com/PapilioVPN/node/internal/modules/xray/routing/domain"
)

// Encode only representable inputs before dispatch, without a second protobuf
// serialization. Collections are borrowed; IP bytes are owned by this request.
func encodeRoutingProbe(probe domain.RoutingProbe) (*native.TestRouteRequest, error) {
	input := probe.Context
	var network netnative.Network
	switch input.Network {
	case domain.RoutingUnknown:
		network = netnative.Network_Unknown
	case domain.RoutingTCP:
		network = netnative.Network_TCP
	case domain.RoutingUDP:
		network = netnative.Network_UDP
	case domain.RoutingUnix:
		network = netnative.Network_UNIX
	default:
		return nil, domain.ErrInvalidRoutingProbe
	}
	for _, value := range []string{input.InboundTag, input.TargetDomain, input.Protocol, input.User} {
		if !utf8.ValidString(value) {
			return nil, domain.ErrInvalidRoutingProbe
		}
	}
	for key, value := range input.Attributes {
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, domain.ErrInvalidRoutingProbe
		}
	}
	for _, selector := range probe.FieldSelectors {
		if !utf8.ValidString(selector) {
			return nil, domain.ErrInvalidRoutingProbe
		}
	}
	source, err := encodeRoutingIPs(input.SourceIPs)
	if err != nil {
		return nil, err
	}
	target, err := encodeRoutingIPs(input.TargetIPs)
	if err != nil {
		return nil, err
	}
	local, err := encodeRoutingIPs(input.LocalIPs)
	if err != nil {
		return nil, err
	}
	return &native.TestRouteRequest{
		RoutingContext: &native.RoutingContext{
			InboundTag: input.InboundTag, Network: network, SourceIPs: source, SourcePort: uint32(input.SourcePort),
			TargetIPs: target, TargetPort: uint32(input.TargetPort), TargetDomain: input.TargetDomain,
			Protocol: input.Protocol, User: input.User, Attributes: input.Attributes,
			LocalIPs: local, LocalPort: uint32(input.LocalPort), VlessRoute: uint32(input.VLESSRoute),
		},
		FieldSelectors: probe.FieldSelectors, PublishResult: probe.PublishResult,
	}, nil
}

func encodeRoutingIPs(values []netip.Addr) ([][]byte, error) {
	if values == nil {
		return nil, nil
	}
	result := make([][]byte, len(values))
	for i, ip := range values {
		if !ip.IsValid() || ip.Zone() != "" {
			return nil, domain.ErrInvalidRoutingProbe
		}
		result[i] = ip.AsSlice()
	}
	return result, nil
}

func decodeRoutingResult(value *native.RoutingContext) (domain.RouteResult, error) {
	if value == nil || len(value.ProtoReflect().GetUnknown()) != 0 || value.SourcePort > 65535 || value.TargetPort > 65535 || value.LocalPort > 65535 || value.VlessRoute > 65535 {
		return domain.RouteResult{}, domain.ErrInvalidRoutingProbeResponse
	}
	var network domain.RoutingNetwork
	switch value.Network {
	case netnative.Network_Unknown:
		network = domain.RoutingUnknown
	case netnative.Network_TCP:
		network = domain.RoutingTCP
	case netnative.Network_UDP:
		network = domain.RoutingUDP
	case netnative.Network_UNIX:
		network = domain.RoutingUnix
	default:
		return domain.RouteResult{}, domain.ErrInvalidRoutingProbeResponse
	}
	source, err := decodeRoutingIPs(value.SourceIPs)
	if err != nil {
		return domain.RouteResult{}, err
	}
	target, err := decodeRoutingIPs(value.TargetIPs)
	if err != nil {
		return domain.RouteResult{}, err
	}
	local, err := decodeRoutingIPs(value.LocalIPs)
	if err != nil {
		return domain.RouteResult{}, err
	}
	return domain.RouteResult{
		Context: domain.RoutingContext{
			InboundTag: value.InboundTag, Network: network, SourceIPs: source, SourcePort: uint16(value.SourcePort),
			TargetIPs: target, TargetPort: uint16(value.TargetPort), TargetDomain: value.TargetDomain,
			Protocol: value.Protocol, User: value.User, Attributes: value.Attributes,
			LocalIPs: local, LocalPort: uint16(value.LocalPort), VLESSRoute: uint16(value.VlessRoute),
		},
		OutboundTag: value.OutboundTag, OutboundGroupTags: value.OutboundGroupTags,
	}, nil
}

func decodeRoutingIPs(values [][]byte) ([]netip.Addr, error) {
	if values == nil {
		return nil, nil
	}
	result := make([]netip.Addr, len(values))
	for i, raw := range values {
		ip, ok := netip.AddrFromSlice(raw)
		if !ok {
			return nil, domain.ErrInvalidRoutingProbeResponse
		}
		result[i] = ip
	}
	return result, nil
}
