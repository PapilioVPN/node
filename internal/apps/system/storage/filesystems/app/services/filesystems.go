// Package services coordinates mount inventory and targeted capacity reads.
package services

import (
	"context"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/domain"
)

type Reader interface {
	List(context.Context, domain.Selection) (domain.Page, error)
	Usage(context.Context, string) (domain.Usage, error)
}
type Service struct{ Reader Reader }

func (service Service) List(ctx context.Context, selection domain.Selection) (domain.ListObservation, error) {
	if selection.Limit > 256 {
		return domain.ListObservation{}, domain.ErrInvalidArgument
	}
	if selection.Limit == 0 {
		selection.Limit = 128
	}
	started := time.Now()
	page, err := service.Reader.List(ctx, selection)
	if err != nil {
		return domain.ListObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ListObservation{}, err
	}
	completed := time.Now()
	return domain.ListObservation{Page: page, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
func (service Service) Usage(ctx context.Context, path string) (domain.UsageObservation, error) {
	if !filepath.IsAbs(path) || len(path) > 4095 || strings.ContainsRune(path, 0) || !utf8.ValidString(path) {
		return domain.UsageObservation{}, domain.ErrInvalidArgument
	}
	started := time.Now()
	usage, err := service.Reader.Usage(ctx, path)
	if err != nil {
		return domain.UsageObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UsageObservation{}, err
	}
	completed := time.Now()
	return domain.UsageObservation{Usage: usage, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
