// Package validators checks public routing input without native effects.
package validators

import (
	"encoding/json"
	"net/netip"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	instancevalidators "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
)

func Text(s string) bool { return utf8.ValidString(s) && len(s) <= 4096 }
func ID(id string) error {
	if instancevalidators.ID(id) != nil {
		return domain.ErrInvalidArgument
	}
	return nil
}
func Tag(tag string) error {
	if tag == "" || !Text(tag) {
		return domain.ErrInvalidArgument
	}
	return nil
}
func Add(r domain.AddRequest) error {
	if ID(r.InstanceID) != nil || (r.Mode != domain.Append && r.Mode != domain.Replace) || (r.Mode == domain.Replace && !r.ConfirmDestructive) || (r.Mode == domain.Append && r.ConfirmDestructive) {
		return domain.ErrInvalidArgument
	}
	if len(r.RuleJSON) == 0 || len(r.RuleJSON) > domain.MaxRuleBytes {
		return domain.ErrInvalidArgument
	}
	var object map[string]json.RawMessage
	if !json.Valid(r.RuleJSON) || json.Unmarshal(r.RuleJSON, &object) != nil || object == nil {
		return domain.ErrInvalidArgument
	}
	return nil
}
func Probe(r domain.ProbeRequest) error {
	if ID(r.InstanceID) != nil || len(r.FieldSelectors) > 1024 {
		return domain.ErrInvalidArgument
	}
	c := r.Context
	if c.Network != domain.UnknownNetwork && c.Network != domain.TCP && c.Network != domain.UDP && c.Network != domain.UNIX {
		return domain.ErrInvalidArgument
	}
	if len(c.SourceIPs)+len(c.TargetIPs)+len(c.LocalIPs) > 4096 || len(c.Attributes) > 1024 || len(c.OutboundGroupTags) > 1024 {
		return domain.ErrInvalidArgument
	}
	for _, list := range [][]string{r.FieldSelectors, c.OutboundGroupTags, {c.InboundTag, c.TargetDomain, c.Protocol, c.User, c.OutboundTag}} {
		for _, s := range list {
			if !Text(s) {
				return domain.ErrInvalidArgument
			}
		}
	}
	for k, v := range c.Attributes {
		if !Text(k) || !Text(v) {
			return domain.ErrInvalidArgument
		}
	}
	for _, list := range [][]netip.Addr{c.SourceIPs, c.TargetIPs, c.LocalIPs} {
		for _, ip := range list {
			if !ip.IsValid() || ip.Zone() != "" {
				return domain.ErrInvalidArgument
			}
		}
	}
	return nil
}
