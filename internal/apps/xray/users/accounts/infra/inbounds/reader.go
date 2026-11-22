// Package inbounds adapts the delivered Inbounds structural observation contract.
package inbounds

import (
	"context"
	"errors"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type Catalog interface {
	Compatibility(context.Context, string, string) (upstream.Compatibility, error)
	List(context.Context, string, upstream.PageRequest) (upstream.Summaries, error)
}
type Reader struct{ Source Catalog }

func projection(s upstream.Summary, generation uint64) domain.Inbound {
	return domain.Inbound{Tag: s.Tag, Protocol: s.Protocol, AccountType: s.AccountType, Reason: s.Reason, EmailMatching: s.EmailMatching, Generation: generation, LiveAccounts: s.LiveAccounts}
}
func (r Reader) Get(ctx context.Context, id, tag string) (domain.Inbound, error) {
	result, err := r.Source.Compatibility(ctx, id, tag)
	if err != nil {
		return domain.Inbound{}, translate(err)
	}
	return projection(result.Inbound, result.Generation), nil
}

// Select performs one native list and zero native calls for its continuations,
// then selects all requested tags in memory. It scans beyond a first match to
// detect ambiguous tags; there is no full list or client per requested account.
func (r Reader) Select(ctx context.Context, id string, tags []string) ([]domain.Inbound, error) {
	selected := make(map[string]domain.Inbound, len(tags))
	for _, tag := range tags {
		selected[tag] = domain.Inbound{Tag: tag, Err: domain.ErrInboundMissing}
	}
	cursor := ""
	count := 0
	generation := uint64(0)
	for {
		page, err := r.Source.List(ctx, id, upstream.PageRequest{Cursor: cursor})
		if err != nil {
			return nil, translate(err)
		}
		if generation != 0 && generation != page.Generation {
			return nil, domain.ErrExecutionChanged
		}
		generation = page.Generation
		count += len(page.Inbounds)
		if count > upstream.MaxRecords || len(page.Inbounds) == 0 && page.NextCursor != "" {
			return nil, domain.ErrInvalidResponse
		}
		for _, summary := range page.Inbounds {
			old, wanted := selected[summary.Tag]
			if !wanted {
				continue
			}
			if old.Err == nil || errors.Is(old.Err, domain.ErrInboundAmbiguous) {
				old.Err = domain.ErrInboundAmbiguous
				selected[summary.Tag] = old
			} else {
				selected[summary.Tag] = projection(summary, generation)
			}
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			return nil, domain.ErrInvalidResponse
		}
		cursor = page.NextCursor
	}
	result := make([]domain.Inbound, len(tags))
	for i, tag := range tags {
		result[i] = selected[tag]
		result[i].Generation = generation
	}
	return result, nil
}
func translate(err error) error {
	var own error
	switch {
	case errors.Is(err, upstream.ErrInboundNotFound):
		own = domain.ErrInboundMissing
	case errors.Is(err, upstream.ErrAmbiguousInbound):
		own = domain.ErrInboundAmbiguous
	case errors.Is(err, upstream.ErrCapacity):
		own = domain.ErrCapacity
	case errors.Is(err, upstream.ErrNotFound):
		own = domain.ErrNotFound
	case errors.Is(err, upstream.ErrExecutionChanged), errors.Is(err, upstream.ErrStale):
		own = domain.ErrExecutionChanged
	case errors.Is(err, upstream.ErrUncertain):
		own = domain.ErrUncertain
	case errors.Is(err, upstream.ErrNotRunning):
		own = domain.ErrNotRunning
	case errors.Is(err, upstream.ErrUnsupported):
		own = domain.ErrUnsupported
	case errors.Is(err, upstream.ErrInvalidArgument):
		own = domain.ErrInvalidArgument
	}
	return errors.Join(own, err)
}
