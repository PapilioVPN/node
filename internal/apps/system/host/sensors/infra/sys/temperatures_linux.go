package sys

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
	"golang.org/x/sync/errgroup"
)

type Reader struct{}

func (Reader) Temperatures(ctx context.Context) ([]domain.Temperature, error) {
	return temperatures(ctx, "/sys")
}
func temperatures(ctx context.Context, root string) ([]domain.Temperature, error) {
	sources, err := channels(ctx, root, "temp")
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return thermal(ctx, root)
	}
	rows := make([]domain.Temperature, len(sources))
	group, work := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for i, source := range sources {
		if work.Err() != nil {
			break
		}
		group.Go(func() error {
			value, err := text(work, source.base+"_input")
			if err != nil {
				return err
			}
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return err
			}
			row := domain.Temperature{ID: source.id, Device: source.device, MilliCelsius: number}
			row.Label, err = optionalText(work, source.base+"_label")
			if err != nil {
				return err
			}
			row.HighMilliCelsius, err = optionalNumber(work, source.base+"_max")
			if err != nil {
				return err
			}
			row.CriticalMilliCelsius, err = optionalNumber(work, source.base+"_crit")
			if err != nil {
				return err
			}
			rows[i] = row
			return nil
		})
	}
	if err = group.Wait(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b domain.Temperature) int { return cmp.Compare(a.ID, b.ID) })
	return rows, nil
}
func thermal(ctx context.Context, root string) ([]domain.Temperature, error) {
	zones, err := entries(ctx, filepath.Join(root, "class/thermal"))
	if err != nil {
		return nil, err
	}
	rows := []domain.Temperature{}
	for _, zone := range zones {
		if !strings.HasPrefix(zone.Name(), "thermal_zone") {
			continue
		}
		path := filepath.Join(root, "class/thermal", zone.Name())
		device, err := text(ctx, filepath.Join(path, "type"))
		if err != nil {
			return nil, err
		}
		value, err := text(ctx, filepath.Join(path, "temp"))
		if err != nil {
			return nil, err
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		rows = append(rows, domain.Temperature{ID: zone.Name(), Device: device, MilliCelsius: number})
	}
	slices.SortFunc(rows, func(a, b domain.Temperature) int { return cmp.Compare(a.ID, b.ID) })
	return rows, ctx.Err()
}
func (Reader) Fans(ctx context.Context) ([]domain.Fan, error) { return fans(ctx, "/sys") }
func fans(ctx context.Context, root string) ([]domain.Fan, error) {
	sources, err := channels(ctx, root, "fan")
	if err != nil {
		return nil, err
	}
	rows := make([]domain.Fan, len(sources))
	group, work := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for i, source := range sources {
		if work.Err() != nil {
			break
		}
		group.Go(func() error {
			value, err := text(work, source.base+"_input")
			if err != nil {
				return err
			}
			rpm, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return err
			}
			label, err := optionalText(work, source.base+"_label")
			if err != nil {
				return err
			}
			rows[i] = domain.Fan{ID: source.id, Device: source.device, Label: label, RPM: rpm}
			return nil
		})
	}
	if err = group.Wait(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b domain.Fan) int { return cmp.Compare(a.ID, b.ID) })
	return rows, nil
}
