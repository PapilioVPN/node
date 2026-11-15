package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

// ListInstanceStatuses observes only the selected page of the immutable ID index.
// Membership is stable for this Node run; individual states may change between
// observations. Binary search skips earlier IDs; observations and result
// allocation never exceed the remaining configured count.
func (s *LifecycleService) ListInstanceStatuses(ctx context.Context, input domain.StatusPageInput) (domain.StatusPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.StatusPage{}, err
	}
	if input.PageSize <= 0 {
		return domain.StatusPage{}, fmt.Errorf("%w: page_size must be positive", domain.ErrInvalidInput)
	}
	start, found := slices.BinarySearch(s.ids, input.AfterID)
	if found {
		start++
	}
	end := start + min(input.PageSize, len(s.ids)-start)
	page := domain.StatusPage{Items: make([]domain.Status, 0, end-start)}
	for _, id := range s.ids[start:end] {
		value, err := s.instances[id].Status(ctx)
		if err != nil {
			return domain.StatusPage{}, err
		}
		page.Items = append(page.Items, value)
	}
	if err := ctx.Err(); err != nil {
		return domain.StatusPage{}, err
	}
	if end < len(s.ids) {
		page.NextAfterID = s.ids[end-1]
	}
	return page, nil
}
