package grpcapi

import (
	"net/netip"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	routingv1 "github.com/PapilioVPN/node/internal/apps/xray/network/routing/grpcapi/proto/v1"
)

func decodeRoutingContext(value *routingv1.RoutingContext) (domain.RoutingContext, error) {
	if value == nil || value.SourcePort > 65535 || value.TargetPort > 65535 || value.LocalPort > 65535 || value.VlessRoute > 65535 {
		return domain.RoutingContext{}, domain.ErrInvalidRoutingProbe
	}
	var network domain.RoutingNetwork
	switch value.Network {
	case routingv1.RoutingNetwork_ROUTING_NETWORK_UNKNOWN:
		network = domain.RoutingUnknown
	case routingv1.RoutingNetwork_ROUTING_NETWORK_TCP:
		network = domain.RoutingTCP
	case routingv1.RoutingNetwork_ROUTING_NETWORK_UDP:
		network = domain.RoutingUDP
	case routingv1.RoutingNetwork_ROUTING_NETWORK_UNIX:
		network = domain.RoutingUnix
	default:
		return domain.RoutingContext{}, domain.ErrInvalidRoutingProbe
	}
	source, err := decodeRoutingIPs(value.SourceIps)
	if err != nil {
		return domain.RoutingContext{}, err
	}
	target, err := decodeRoutingIPs(value.TargetIps)
	if err != nil {
		return domain.RoutingContext{}, err
	}
	local, err := decodeRoutingIPs(value.LocalIps)
	if err != nil {
		return domain.RoutingContext{}, err
	}
	return domain.RoutingContext{
		InboundTag: value.InboundTag, Network: network, SourceIPs: source, SourcePort: uint16(value.SourcePort),
		TargetIPs: target, TargetPort: uint16(value.TargetPort), TargetDomain: value.TargetDomain,
		Protocol: value.Protocol, User: value.User, Attributes: value.Attributes,
		LocalIPs: local, LocalPort: uint16(value.LocalPort), VLESSRoute: uint16(value.VlessRoute),
	}, nil
}

func decodeRoutingIPs(values []string) ([]netip.Addr, error) {
	if values == nil {
		return nil, nil
	}
	result := make([]netip.Addr, len(values))
	for i, value := range values {
		ip, err := netip.ParseAddr(value)
		if err != nil || ip.Zone() != "" {
			return nil, domain.ErrInvalidRoutingProbe
		}
		result[i] = ip
	}
	return result, nil
}

func encodeRoutingContext(value domain.RoutingContext) *routingv1.RoutingContext {
	// These four wire enum values match the domain enum; the native enum is
	// translated separately at the SDK boundary. No SDK message crosses app.
	return &routingv1.RoutingContext{
		InboundTag: value.InboundTag, Network: routingv1.RoutingNetwork(value.Network),
		SourceIps: encodeRoutingIPs(value.SourceIPs), SourcePort: uint32(value.SourcePort),
		TargetIps: encodeRoutingIPs(value.TargetIPs), TargetPort: uint32(value.TargetPort), TargetDomain: value.TargetDomain,
		Protocol: value.Protocol, User: value.User, Attributes: value.Attributes,
		LocalIps: encodeRoutingIPs(value.LocalIPs), LocalPort: uint32(value.LocalPort), VlessRoute: uint32(value.VLESSRoute),
	}
}

func encodeRoutingIPs(values []netip.Addr) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.String()
	}
	return result
}
