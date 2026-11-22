package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type recordPage struct {
	records domain.Records
	expires time.Time
	bytes   int
}

func token() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(data[:])
}
func cloneRecords(records []domain.Record) []domain.Record {
	result := make([]domain.Record, len(records))
	for i, r := range records {
		result[i] = domain.Record{Tag: r.Tag, Configuration: append([]byte{}, r.Configuration...)}
	}
	return result
}

// List retains one bounded native observation for continuation, with no repeated
// native read per page. A new successful scan replaces this instance's old scan.
func (s *Service) List(ctx context.Context, id string, request domain.PageRequest) (domain.Records, error) {
	size := request.PageSize
	if size == 0 {
		size = domain.MaxPageSize
	}
	if size > domain.MaxPageSize || len(request.PageToken) > 128 {
		return domain.Records{}, domain.ErrInvalidArgument
	}
	selected, generation, release, err := s.beginRead(ctx, id)
	if err != nil {
		return domain.Records{}, err
	}
	defer release()
	if request.PageToken != "" {
		return s.continueRecords(ctx, id, request.PageToken, int(size))
	}
	started := time.Now()
	native, err := selected.client.Records(ctx)
	if err = s.finishRead(ctx, id, generation, err); err != nil {
		return domain.Records{}, err
	}
	records := domain.Records{InstanceID: id, Generation: generation, Outbounds: native, ObservedAt: time.Now().UTC(), Duration: time.Since(started)}
	if len(records.Outbounds) > domain.MaxRecords {
		return domain.Records{}, domain.ErrCapacity
	}
	retained := 0
	for _, r := range records.Outbounds {
		retained += len(r.Tag) + len(r.Configuration) + 48
	}
	if retained > domain.MaxResponseBytes {
		return domain.Records{}, domain.ErrCapacity
	}
	records.Source = domain.Source
	records.DefaultState = "unobservable"
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	used := 0
	otherScans := 0
	for key, page := range s.pages {
		if time.Now().After(page.expires) {
			delete(s.pages, key)
		} else if page.records.InstanceID != id {
			used += page.bytes
			otherScans++
		}
	}
	if len(records.Outbounds) > int(size) && (otherScans >= 32 || used+retained > domain.MaxResponseBytes) {
		return domain.Records{}, domain.ErrCapacity
	}
	for key, page := range s.pages {
		if page.records.InstanceID == id {
			delete(s.pages, key)
		}
	}
	if len(records.Outbounds) <= int(size) {
		records.Outbounds = cloneRecords(records.Outbounds)
		return records, nil
	}
	key := token()
	saved := records
	saved.InstanceID = strings.Clone(id)
	s.pages[key] = recordPage{records: saved, expires: time.Now().Add(time.Minute), bytes: retained}
	records.Outbounds = cloneRecords(records.Outbounds[:size])
	records.NextPageToken = key + ":" + strconv.Itoa(int(size))
	return records, nil
}
func (s *Service) continueRecords(ctx context.Context, id, key string, size int) (domain.Records, error) {
	identity, position, ok := strings.Cut(key, ":")
	offset, err := strconv.Atoi(position)
	if !ok || err != nil || offset <= 0 {
		return domain.Records{}, domain.ErrInvalidArgument
	}
	state, err := s.status.Status(ctx, id)
	if err != nil {
		return domain.Records{}, err
	}
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	saved, ok := s.pages[identity]
	if !ok || time.Now().After(saved.expires) || saved.records.InstanceID != id {
		return domain.Records{}, domain.ErrContinuation
	}
	if state.Execution.State != instances.Running || state.Execution.Generation != saved.records.Generation {
		delete(s.pages, identity)
		return domain.Records{}, domain.ErrContinuation
	}
	if offset >= len(saved.records.Outbounds) {
		return domain.Records{}, domain.ErrInvalidArgument
	}
	result := saved.records
	end := min(offset+size, len(result.Outbounds))
	result.Outbounds = cloneRecords(result.Outbounds[offset:end])
	if end < len(saved.records.Outbounds) {
		result.NextPageToken = identity + ":" + strconv.Itoa(end)
	}
	return result, ctx.Err()
}
