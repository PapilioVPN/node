// Package services coordinates distinct logind and legacy login observations.
package services

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
)

type LogindReader interface {
	List(context.Context, domain.Selection) (domain.Page, error)
	Read(context.Context, string) (domain.Details, error)
}
type RecordsReader interface {
	List(context.Context, domain.Selection) (domain.RecordPage, error)
}
type Service struct {
	Logind  LogindReader
	Records RecordsReader
}

func selection(input domain.Selection) (domain.Selection, error) {
	if input.Offset > 16384 || input.Limit > 256 {
		return input, domain.ErrInvalidArgument
	}
	if input.Limit == 0 {
		input.Limit = 128
	}
	return input, nil
}
func (service Service) List(ctx context.Context, input domain.Selection) (domain.ListObservation, error) {
	input, err := selection(input)
	if err != nil {
		return domain.ListObservation{}, err
	}
	started := time.Now()
	page, err := service.Logind.List(ctx, input)
	if err != nil {
		return domain.ListObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ListObservation{}, err
	}
	completed := time.Now()
	return domain.ListObservation{Page: page, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
func (service Service) Read(ctx context.Context, id string) (domain.ReadObservation, error) {
	if id == "" || len(id) > 255 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
		return domain.ReadObservation{}, domain.ErrInvalidArgument
	}
	started := time.Now()
	details, err := service.Logind.Read(ctx, id)
	if err != nil {
		return domain.ReadObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ReadObservation{}, err
	}
	completed := time.Now()
	return domain.ReadObservation{Details: details, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
func (service Service) ListRecords(ctx context.Context, input domain.Selection) (domain.RecordsObservation, error) {
	input, err := selection(input)
	if err != nil {
		return domain.RecordsObservation{}, err
	}
	started := time.Now()
	page, err := service.Records.List(ctx, input)
	if err != nil {
		return domain.RecordsObservation{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.RecordsObservation{}, err
	}
	completed := time.Now()
	return domain.RecordsObservation{Page: page, ObservedAt: completed.UTC(), Duration: completed.Sub(started)}, nil
}
