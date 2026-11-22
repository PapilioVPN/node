// Package ports declares sync's consuming contracts, independent of adapters.
package ports

import (
	"context"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// MembershipV1 is an explicit selected-membership overlay on one desired config
// revision. Configs owns original sources, compilation, private durable state,
// execution replacement, readiness and recovery. These operations are not an
// atomic filesystem/engine transaction. The approval token must bind instance,
// revision, selection and candidate; it must not be a public credential hash.
type MembershipV1 struct {
	InstanceID       string
	OperationID      string
	ExpectedRevision uint64
	Generation       uint64
	Sets             []accounts.InboundUserSet
}
type ReviewV1 struct {
	Token  string
	Mode   string
	Reason string
}
type ResultV1 struct {
	Revision   uint64
	Generation uint64
	Effect     accounts.Effect
	Durable    bool
	// Ready is native API readiness, not payload traffic or endpoint attestation.
	Ready            bool
	RequiresRecovery bool
}

// ConfigsV1 must reject stale input, preserve known publication/unknown effects,
// and never replay an ambiguous write. Recover is explicit previous-config
// restoration, not an automatic retry. Result readback remains available after
// response loss. A provider that cannot preserve source semantics must reject
// that input before publishing. V1 is local composition, not a new wire API.
type ConfigsV1 interface {
	Revision(context.Context, string) (uint64, error)
	PlanMembership(context.Context, MembershipV1) (ReviewV1, error)
	ApplyMembership(context.Context, string, string) (ResultV1, error)
	MembershipStatus(context.Context, string, string) (ResultV1, error)
	RecoverMembership(context.Context, string, string, uint64) (ResultV1, error)
}
