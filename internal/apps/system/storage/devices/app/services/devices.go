// Package services coordinates bounded block-device I/O observations.
package services

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
)

type Reader interface {
	List(context.Context, domain.Selection) (domain.Page, error)
}
type Service struct{ Reader Reader }

func (service Service) List(ctx context.Context, selection domain.Selection) (domain.Observation, error) {
	if selection.Limit > 256 || len(selection.AfterName) > 255 || !utf8.ValidString(selection.AfterName) || strings.ContainsRune(selection.AfterName, 0) {
		return domain.Observation{}, domain.ErrInvalidArgument
	}
	if selection.Limit == 0 {
		selection.Limit = 128
	}
	started := time.Now()
	page, err := service.Reader.List(ctx, selection)
	if err != nil {
		return domain.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Observation{}, err
	}
	completed := time.Now()
	return domain.Observation{Page: page, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
