package policies

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

// Full membership is an ordered native configuration collection. In contrast
// to a named live delta it can retain anonymous users and the duplicate email
// memberships supported by WireGuard, Hysteria and legacy Shadowsocks.
func full(ctx context.Context, in domain.Inbound, raws [][]byte, decoder Decoder) (domain.Difference, []domain.Step, error) {
	d := domain.Difference{InboundTag: in.Tag}
	old := make(map[string][]domain.Member)
	next := make(map[string][]domain.Member)
	key := func(u domain.Member) (string, error) {
		if u.Email == "" {
			return "\x00" + string(u.Fingerprint[:]), nil
		}
		return Identity(u.Email, in.EmailMatching)
	}
	for _, u := range in.Users {
		k, err := key(u)
		if err != nil {
			return d, nil, err
		}
		old[k] = append(old[k], u)
	}
	var desired []domain.Member
	for _, raw := range raws {
		if err := ctx.Err(); err != nil {
			return d, nil, err
		}
		u, err := decoder.Decode(raw)
		if err != nil {
			return d, nil, err
		}
		if u.AccountType != in.AccountType {
			return d, nil, domain.ErrInvalid
		}
		k, err := key(u)
		if err != nil {
			return d, nil, err
		}
		if u.Email != "" && len(next[k]) != 0 && in.AccountType != "xray.proxy.wireguard.PeerConfig" && in.AccountType != "xray.proxy.shadowsocks.Account" && in.AccountType != "xray.proxy.hysteria.account.Account" {
			return d, nil, domain.ErrInvalid
		}
		next[k] = append(next[k], u)
		desired = append(desired, u)
	}
	unchanged := make(map[string]bool)
	for k, users := range next {
		prior := old[k]
		if len(users) == 1 && len(prior) == 1 && users[0].Comparable && prior[0].Comparable && users[0].Fingerprint == prior[0].Fingerprint {
			unchanged[k] = true
			d.Unchanged++
			continue
		}
		d.Replaced += min(len(prior), len(users))
		d.Added += max(0, len(users)-len(prior))
	}
	for k, users := range old {
		d.Removed += max(0, len(users)-len(next[k]))
	}
	var steps []domain.Step
	for _, u := range in.Users {
		k, _ := key(u)
		if !unchanged[k] {
			steps = append(steps, domain.Step{InboundTag: in.Tag, Email: u.Email, Action: domain.Remove})
		}
	}
	for _, u := range desired {
		k, _ := key(u)
		if !unchanged[k] {
			steps = append(steps, domain.Step{InboundTag: in.Tag, Email: u.Email, Action: domain.Add, Configuration: u.Configuration})
		}
	}
	return d, steps, nil
}
