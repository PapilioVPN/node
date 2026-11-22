// Package configs adapts Configs' reviewed selected-membership contract.
package configs

import (
	"context"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
)

// NativeV1 is implemented by Configs' Memberships coordinator. It compiles once
// using its original sources and the existing Accounts transformation; the
// Configs owner alone publishes, replaces, waits for readiness and restores.
type NativeV1 interface {
	Revision(context.Context, string) (uint64, error)
	Plan(context.Context, string, uint64, uint64, []accounts.InboundUserSet) (upstream.Plan, error)
	Apply(context.Context, string, string) (upstream.Application, error)
	Application(context.Context, string, string) (upstream.Application, error)
	Recover(context.Context, string, string, uint64) (upstream.Application, error)
}
type V1 struct{ Source NativeV1 }

var _ ports.ConfigsV1 = V1{}

func (v V1) Revision(ctx context.Context, id string) (uint64, error) {
	return v.Source.Revision(ctx, id)
}
func (v V1) PlanMembership(ctx context.Context, r ports.MembershipV1) (ports.ReviewV1, error) {
	p, err := v.Source.Plan(ctx, r.InstanceID, r.ExpectedRevision, r.Generation, r.Sets)
	if err != nil {
		return ports.ReviewV1{}, err
	}
	return ports.ReviewV1{Token: p.ID, Mode: string(p.Mode), Reason: p.Reason}, nil
}
func (v V1) ApplyMembership(ctx context.Context, id, token string) (ports.ResultV1, error) {
	r, err := v.Source.Apply(ctx, id, token)
	return result(r), err
}
func (v V1) MembershipStatus(ctx context.Context, id, token string) (ports.ResultV1, error) {
	r, err := v.Source.Application(ctx, id, token)
	return result(r), err
}
func (v V1) RecoverMembership(ctx context.Context, id, token string, expected uint64) (ports.ResultV1, error) {
	r, err := v.Source.Recover(ctx, id, token, expected)
	return result(r), err
}
func result(a upstream.Application) ports.ResultV1 {
	r := ports.ResultV1{Revision: a.Revision, Generation: a.Generation, Durable: a.Persistence.Durable && a.ReceiptDurable, Ready: a.Readiness == "api_ready", RequiresRecovery: a.RequiresRecovery}
	outcome, effect := a.Outcome, a.EngineEffect
	published := a.Persistence.Published || a.Persistence.PublicationUnknown
	publicationUnknown := a.Persistence.PublicationUnknown
	if a.Recovery != nil {
		rec := a.Recovery
		r.Revision, r.Generation = rec.Revision, rec.Generation
		r.Durable = rec.Persistence.Durable && a.ReceiptDurable
		r.Ready = rec.Readiness == "api_ready"
		outcome, effect = rec.Outcome, rec.EngineEffect
		published = rec.Persistence.Published || rec.Persistence.PublicationUnknown
		publicationUnknown = rec.Persistence.PublicationUnknown
	}
	switch {
	case publicationUnknown:
		r.Effect = accounts.Unknown
	case outcome == "completed" || outcome == "unchanged":
		r.Effect = accounts.Completed
	case effect == "unknown" || effect == "in_progress" || outcome == "in_progress" || outcome == "unknown" || outcome == "recovery_required":
		r.Effect = accounts.Unknown
	case effect == "completed" || effect == "partial" || published:
		r.Effect = accounts.Partial
	default:
		r.Effect = accounts.NotApplied
	}
	return r
}
