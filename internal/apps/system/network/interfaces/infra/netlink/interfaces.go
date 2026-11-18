package netlink

import (
	"errors"
	"math"
	"net"
	"net/netip"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/domain"
	native "github.com/vishvananda/netlink"
)

func linkAttributes(link native.Link) (*native.LinkAttrs, error) {
	if link == nil {
		return nil, errors.New("missing interface")
	}
	attrs := link.Attrs()
	if attrs == nil || attrs.Index <= 0 || uint64(attrs.Index) > math.MaxUint32 || attrs.Name == "" || !utf8.ValidString(attrs.Name) {
		return nil, errors.New("invalid interface identity")
	}
	return attrs, nil
}

func interfaceRecord(link native.Link) (domain.Interface, error) {
	attrs, err := linkAttributes(link)
	if err != nil {
		return domain.Interface{}, err
	}
	if attrs.MTU < 0 || uint64(attrs.MTU) > math.MaxUint32 || attrs.ParentIndex < 0 || uint64(attrs.ParentIndex) > math.MaxUint32 || attrs.MasterIndex < 0 || uint64(attrs.MasterIndex) > math.MaxUint32 {
		return domain.Interface{}, errors.New("invalid interface dimensions")
	}
	var parentNamespace *int32
	if attrs.NetNsID < -1 || int64(attrs.NetNsID) > math.MaxInt32 {
		return domain.Interface{}, errors.New("invalid parent network namespace ID")
	}
	if attrs.NetNsID >= 0 {
		value := int32(attrs.NetNsID)
		parentNamespace = &value
	}
	kind := link.Type()
	if !utf8.ValidString(kind) {
		return domain.Interface{}, errors.New("invalid interface kind encoding")
	}
	return domain.Interface{
		Index: uint32(attrs.Index), Name: attrs.Name, Kind: kind, MTU: uint32(attrs.MTU),
		HardwareAddress: attrs.HardwareAddr.String(), Flags: attrs.RawFlags,
		OperationalState: uint32(attrs.OperState), ParentNamespaceID: parentNamespace,
		ParentIndex: uint32(attrs.ParentIndex), MasterIndex: uint32(attrs.MasterIndex),
		Addresses: []domain.Address{},
	}, nil
}

func addressRecord(value native.Addr) (domain.Address, error) {
	if value.LinkIndex <= 0 || uint64(value.LinkIndex) > math.MaxUint32 || value.Flags < 0 || uint64(value.Flags) > math.MaxUint32 || value.Scope < 0 || uint64(value.Scope) > math.MaxUint32 || value.PreferedLft < 0 || uint64(value.PreferedLft) > math.MaxUint32 || value.ValidLft < 0 || uint64(value.ValidLft) > math.MaxUint32 || !utf8.ValidString(value.Label) {
		return domain.Address{}, errors.New("invalid interface address attributes")
	}
	prefix, err := addressPrefix(value.IPNet)
	if err != nil {
		return domain.Address{}, err
	}
	family := "ipv6"
	if prefix.Addr().Is4() {
		family = "ipv4"
	}
	row := domain.Address{InterfaceIndex: uint32(value.LinkIndex), Family: family, LocalPrefix: prefix.String(), Label: value.Label, Flags: uint32(value.Flags), Scope: uint32(value.Scope), PreferredLifetimeSeconds: uint32(value.PreferedLft), ValidLifetimeSeconds: uint32(value.ValidLft)}
	if value.Peer != nil {
		peer, err := addressPrefix(value.Peer)
		if err != nil {
			return domain.Address{}, err
		}
		if peer.Addr().Is4() != prefix.Addr().Is4() {
			return domain.Address{}, errors.New("peer address family mismatch")
		}
		text := peer.String()
		row.PeerPrefix = &text
	}
	if len(value.Broadcast) > 0 {
		addr, ok := netip.AddrFromSlice(value.Broadcast)
		if !ok {
			return domain.Address{}, errors.New("invalid broadcast address")
		}
		text := addr.Unmap().String()
		row.Broadcast = &text
	}
	return row, nil
}

func addressPrefix(value *net.IPNet) (netip.Prefix, error) {
	if value == nil {
		return netip.Prefix{}, errors.New("missing interface address")
	}
	ones, bits := value.Mask.Size()
	switch bits {
	case 32:
		ip := value.IP.To4()
		if ip != nil {
			return netip.PrefixFrom(netip.AddrFrom4([4]byte(ip)), ones), nil
		}
	case 128:
		ip := value.IP.To16()
		if ip != nil {
			return netip.PrefixFrom(netip.AddrFrom16([16]byte(ip)), ones), nil
		}
	}
	return netip.Prefix{}, errors.New("invalid interface address or mask")
}
