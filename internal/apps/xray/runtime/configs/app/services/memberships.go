package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type MembershipTransform func(context.Context, []byte, []accounts.InboundUserSet) ([]byte, error)
type SourceEmbedding func([]domain.Source) bool

// Memberships is Configs' narrow local composition contract for Sync. It reuses
// accounts' complete native transformation, with one original-source compilation.
// Native protobuf can replace source collections only when absence of source Env
// is proven. Unsupported source context rejects BEFORE publication. Full selected
// membership forces execution replacement: desired equivalence alone cannot
// attest that separate live account changes have not drifted from those sources.
type Memberships struct {
	*Plans
	transform  MembershipTransform
	embeddable SourceEmbedding
}

func NewMemberships(plans *Plans, transform MembershipTransform, embeddable SourceEmbedding) (*Memberships, error) {
	if plans == nil || transform == nil || embeddable == nil {
		return nil, domain.ErrInvalidArgument
	}
	return &Memberships{Plans: plans, transform: transform, embeddable: embeddable}, nil
}
func (m *Memberships) Revision(ctx context.Context, id string) (uint64, error) {
	metadata, err := m.Plans.state.Metadata(ctx, id)
	return metadata.Revision, err
}
func (m *Memberships) Plan(ctx context.Context, id string, expected, generation uint64, sets []accounts.InboundUserSet) (domain.Plan, error) {
	if expected == math.MaxUint64 {
		return domain.Plan{}, domain.ErrCapacity
	}
	ctx, _, release, err := m.Plans.state.begin(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	defer release()
	entry, err := m.Plans.selected(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	state, err := m.Plans.runtime.Status(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if state.Execution.Generation != generation {
		return domain.Plan{}, domain.ErrStalePlan
	}
	previous, err := m.Plans.previous(ctx, entry, expected)
	if err != nil {
		return domain.Plan{}, err
	}
	if !m.embeddable(previous.Sources) {
		return domain.Plan{}, domain.ErrUnsupported
	}
	compiled, err := m.Plans.compiler.Compile(ctx, id, previous.Sources)
	if err != nil {
		return domain.Plan{}, err
	}
	content, err := m.transform(ctx, compiled.Content, sets)
	if err != nil {
		switch {
		case errors.Is(err, accounts.ErrInvalidArgument):
			err = errors.Join(domain.ErrInvalidArgument, err)
		case errors.Is(err, accounts.ErrCapacity):
			err = errors.Join(domain.ErrTooLarge, err)
		case errors.Is(err, accounts.ErrUnsupported):
			err = errors.Join(domain.ErrUnsupported, err)
		}
		return domain.Plan{}, err
	}
	sources := []domain.Source{{Name: "membership.pb", Content: content}}
	if err := validators.Sources(sources); err != nil {
		return domain.Plan{}, err
	}
	current, err := m.Plans.runtime.Status(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if current.Execution.Generation != generation || current.Execution.ExecutionID != state.Execution.ExecutionID {
		return domain.Plan{}, domain.ErrStalePlan
	}
	plan, err := m.Plans.review(ctx, id, entry, previous, sources, compiled.Content, content, len(sets) > 0)
	if err == nil && (plan.Generation != generation || plan.ExecutionID != state.Execution.ExecutionID) {
		m.Plans.mu.Lock()
		if entry.pending != nil && entry.pending.plan.ID == plan.ID {
			entry.pending = nil
		}
		m.Plans.mu.Unlock()
		return domain.Plan{}, domain.ErrStalePlan
	}
	return plan, err
}

// Recover derives one stable recovery identity from the opaque review token and
// expected revision, so Sync's four-argument recovery port can safely repeat it
// across response loss or Node restart without triggering another restart.
func (m *Memberships) Recover(ctx context.Context, id, token string, expected uint64) (domain.Application, error) {
	digest := sha256.Sum256([]byte("papilio-config-membership-recovery-v1\x00" + token + "\x00" + strconv.FormatUint(expected, 10)))
	return m.Plans.Recover(ctx, id, token, expected, hex.EncodeToString(digest[:16]))
}
