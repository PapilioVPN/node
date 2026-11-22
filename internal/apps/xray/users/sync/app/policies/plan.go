package policies

import (
	"context"
	"strings"
	"unicode"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

type Decoder interface {
	Decode([]byte) (domain.Member, error)
}

// Identity follows the pinned native email matcher. Legacy Shadowsocks first
// lowercases the query and then EqualFolds it against stored email; identities
// whose own spelling cannot be selected are rejected before any effect.
func Identity(email, matching string) (string, error) {
	if !Identifier(email) {
		return "", domain.ErrInvalid
	}
	switch matching {
	case "exact":
		return email, nil
	case "lowercase":
		return strings.ToLower(email), nil
	case "native_equal_fold":
		if !strings.EqualFold(email, strings.ToLower(email)) {
			return "", domain.ErrUnsupported
		}
		return strings.Map(func(r rune) rune {
			smallest := r
			for n := unicode.SimpleFold(r); n != r; n = unicode.SimpleFold(n) {
				if n < smallest {
					smallest = n
				}
			}
			return smallest
		}, email), nil
	default:
		return "", domain.ErrUnsupported
	}
}

func Build(ctx context.Context, r domain.Request, snapshot domain.Snapshot, decoder Decoder) (domain.Plan, error) {
	if err := Validate(ctx, r); err != nil {
		return domain.Plan{}, err
	}
	if r.Expected != snapshot.Revision {
		return domain.Plan{}, domain.ErrStale
	}
	if decoder == nil {
		return domain.Plan{}, domain.ErrInvalid
	}
	inbounds := make(map[string]domain.Inbound, len(snapshot.Inbounds))
	for _, in := range snapshot.Inbounds {
		if _, ok := inbounds[in.Tag]; ok {
			return domain.Plan{}, domain.ErrInvalid
		}
		inbounds[in.Tag] = in
	}
	sets := make(map[string][][]byte, len(r.Sets))
	for _, set := range r.Sets {
		sets[set.InboundTag] = set.Accounts
	}
	removals := make(map[string][]string)
	for _, item := range r.Removals {
		removals[item.InboundTag] = append(removals[item.InboundTag], item.Email)
	}
	p := domain.Plan{Request: r, Snapshot: snapshot, Preview: domain.Preview{InstanceID: r.InstanceID, OperationID: r.OperationID, Revision: snapshot.Revision, Mode: r.Mode, ApplyMode: "live", Reason: "ordered_non_atomic_accounts"}}
	p.Preview.ObservationBasis = "native_manager_readback"
	if r.Mode == domain.Full {
		p.Preview.ApplyMode, p.Preview.Reason = "replace_execution", "durable_configuration_membership"
	}
	for _, tag := range r.Selection {
		if err := ctx.Err(); err != nil {
			return domain.Plan{}, err
		}
		in, ok := inbounds[tag]
		if !ok || in.AccountType == "" {
			return domain.Plan{}, domain.ErrInvalid
		}
		if r.Mode == domain.Delta && !in.Live {
			return domain.Plan{}, domain.ErrUnsupported
		}
		if r.Mode == domain.Full {
			difference, steps, err := full(ctx, in, sets[tag], decoder)
			if err != nil {
				return domain.Plan{}, err
			}
			p.Preview.Differences = append(p.Preview.Differences, difference)
			p.Steps = append(p.Steps, steps...)
			continue
		}
		old := make(map[string][]domain.Member, len(in.Users))
		order := make([]string, 0, len(in.Users))
		for _, u := range in.Users {
			// Anonymous native credentials cannot be selected by this named
			// delta, but do not prevent reconciliation of unrelated names.
			if u.Email == "" {
				continue
			}
			key, err := Identity(u.Email, in.EmailMatching)
			if err != nil {
				return domain.Plan{}, err
			}
			if len(old[key]) == 0 {
				order = append(order, key)
			}
			old[key] = append(old[key], u)
		}
		diff := domain.Difference{InboundTag: tag}
		seen := make(map[string]bool)
		var remove, add []domain.Step
		removeKey := func(key string) {
			for _, u := range old[key] {
				remove = append(remove, domain.Step{InboundTag: tag, Email: u.Email, Action: domain.Remove})
			}
		}
		for _, email := range removals[tag] {
			key, err := Identity(email, in.EmailMatching)
			if err != nil {
				return domain.Plan{}, err
			}
			if seen[key] {
				return domain.Plan{}, domain.ErrInvalid
			}
			seen[key] = true
			removeKey(key)
			diff.Removed += len(old[key])
		}
		for _, raw := range sets[tag] {
			if err := ctx.Err(); err != nil {
				return domain.Plan{}, err
			}
			u, err := decoder.Decode(raw)
			if err != nil {
				return domain.Plan{}, err
			}
			if u.AccountType != in.AccountType {
				return domain.Plan{}, domain.ErrInvalid
			}
			key, err := Identity(u.Email, in.EmailMatching)
			if err != nil {
				return domain.Plan{}, err
			}
			if seen[key] {
				return domain.Plan{}, domain.ErrInvalid
			}
			seen[key] = true
			prior := old[key]
			if len(prior) == 1 && u.Comparable && prior[0].Comparable && u.Fingerprint == prior[0].Fingerprint {
				diff.Unchanged++
				continue
			}
			if len(prior) != 0 {
				removeKey(key)
				diff.Replaced++
			} else {
				diff.Added++
			}
			add = append(add, domain.Step{InboundTag: tag, Email: u.Email, Action: domain.Add, Configuration: u.Configuration})
		}
		if r.Mode == domain.Full {
			for _, key := range order {
				if !seen[key] {
					removeKey(key)
					diff.Removed += len(old[key])
				}
			}
		}
		// Remove every matching old peer before adding its replacement. This
		// also clears WireGuard PSKs that a native upsert could silently retain.
		p.Steps = append(p.Steps, remove...)
		p.Steps = append(p.Steps, add...)
		p.Preview.Differences = append(p.Preview.Differences, diff)
	}
	if r.Mode == domain.Delta && len(p.Steps) > 2*1000 {
		return domain.Plan{}, domain.ErrCapacity
	}
	return p, ctx.Err()
}
