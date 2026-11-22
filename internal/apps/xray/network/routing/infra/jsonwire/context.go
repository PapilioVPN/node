package jsonwire

import (
	"net/netip"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

type Context struct {
	InboundTag        string            `json:"inbound_tag"`
	Network           domain.Network    `json:"network"`
	SourceIPs         []netip.Addr      `json:"source_ips"`
	TargetIPs         []netip.Addr      `json:"target_ips"`
	LocalIPs          []netip.Addr      `json:"local_ips"`
	SourcePort        uint16            `json:"source_port"`
	TargetPort        uint16            `json:"target_port"`
	LocalPort         uint16            `json:"local_port"`
	VLESSRoute        uint16            `json:"vless_route"`
	TargetDomain      string            `json:"target_domain"`
	Protocol          string            `json:"protocol"`
	User              string            `json:"user"`
	Attributes        map[string]string `json:"attributes"`
	OutboundGroupTags []string          `json:"outbound_group_tags"`
	OutboundTag       string            `json:"outbound_tag"`
}

func (c Context) domain() domain.Context { return domain.Context(c) }
