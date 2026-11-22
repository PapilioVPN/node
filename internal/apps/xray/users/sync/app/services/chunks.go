// Package services coordinates bounded scoped account synchronization.
package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"sync"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

const maxChunks = 2048

type Chunk struct {
	Sequence  uint32
	Additions []accounts.Addition
	Removals  []accounts.Removal
	Final     bool
}
type TransferStatus struct {
	ID           string
	InstanceID   string
	NextSequence uint32
	Final        bool
	ExpiresAt    time.Time
}
type transfer struct {
	status                                     TransferStatus
	request                                    domain.Request
	digest                                     [32]byte
	items, bytes, expectedItems, expectedBytes int
	reservation                                int
	chunks                                     map[uint32][32]byte
	sets                                       map[string]int
}

// Transfers retains at most eight transfers for an absolute 30 seconds, with a
// 32 MiB global logical reservation (payload, selectors, receipts and records).
// Expiry is checked on access; no background goroutine or partial live effect.
// Finish only returns owned input for the ordinary preview operation.
type Transfers struct {
	mu       sync.Mutex
	entries  map[string]*transfer
	reserved int
	now      func() time.Time
}

func NewTransfers() *Transfers { return &Transfers{entries: make(map[string]*transfer), now: time.Now} }
func (s *Transfers) expire() {
	for id, entry := range s.entries {
		if !s.now().Before(entry.status.ExpiresAt) {
			s.reserved -= entry.reservation
			delete(s.entries, id)
		}
	}
}
func transferReservation(r domain.Request, count, size int) int {
	result := size + count*128 + maxChunks*40 + len(r.InstanceID) + len(r.OperationID) + 256
	for _, tag := range r.Selection {
		result += len(tag)*2 + 64
	}
	return result
}
func (s *Transfers) Begin(ctx context.Context, r domain.Request, items, size int) (TransferStatus, error) {
	if len(r.Sets) != 0 || len(r.Removals) != 0 || items < 0 || size < 0 {
		return TransferStatus{}, domain.ErrInvalid
	}
	// A full transfer declares each selected collection up front, including empty.
	for _, tag := range r.Selection {
		r.Sets = append(r.Sets, accounts.InboundUserSet{InboundTag: tag, Accounts: [][]byte{}})
	}
	if err := policies.Validate(ctx, r); err != nil {
		return TransferStatus{}, err
	}
	if items > domain.MaxUsers || size > domain.MaxBytes || r.Mode == domain.Delta && (items > accounts.MaxBatchItems || size > accounts.MaxBatchBytes) {
		return TransferStatus{}, domain.ErrCapacity
	}
	reservation := transferReservation(r, items, size)
	digest := policies.Digest(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	for _, entry := range s.entries {
		if entry.request.InstanceID == r.InstanceID && entry.request.OperationID == r.OperationID {
			if entry.digest != digest || entry.expectedItems != items || entry.expectedBytes != size {
				return TransferStatus{}, domain.ErrConflict
			}
			return entry.status, nil
		}
	}
	if len(s.entries) >= domain.MaxTransfers || reservation > domain.MaxRetainedBytes-s.reserved {
		return TransferStatus{}, domain.ErrCapacity
	}
	r = policies.Own(r)
	st := TransferStatus{ID: rand.Text(), InstanceID: r.InstanceID, ExpiresAt: s.now().UTC().Add(domain.Lifetime)}
	entry := &transfer{status: st, request: r, digest: digest, expectedItems: items, expectedBytes: size, reservation: reservation, chunks: make(map[uint32][32]byte), sets: make(map[string]int)}
	for i, set := range r.Sets {
		entry.sets[set.InboundTag] = i
	}
	s.entries[st.ID] = entry
	s.reserved += reservation
	return st, nil
}
func chunkDigest(c Chunk) [32]byte {
	r := domain.Request{Mode: domain.Delta, OperationID: "more"}
	if c.Final {
		r.OperationID = "final"
	}
	for _, a := range c.Additions {
		r.Sets = append(r.Sets, accounts.InboundUserSet{InboundTag: a.InboundTag, Accounts: [][]byte{a.Configuration}})
	}
	r.Removals = c.Removals
	return policies.Digest(r)
}
func (s *Transfers) Append(ctx context.Context, instance, id string, c Chunk) (TransferStatus, error) {
	if err := ctx.Err(); err != nil {
		return TransferStatus{}, err
	}
	if len(c.Additions)+len(c.Removals) > domain.MaxChunkItems || c.Sequence >= maxChunks {
		return TransferStatus{}, domain.ErrCapacity
	}
	size := 0
	for _, a := range c.Additions {
		size += len(a.Configuration)
		if len(a.Configuration) == 0 {
			return TransferStatus{}, domain.ErrInvalid
		}
	}
	for _, r := range c.Removals {
		size += len(r.Email)
		if !policies.Identifier(r.Email) {
			return TransferStatus{}, domain.ErrInvalid
		}
	}
	if size > domain.MaxChunkBytes {
		return TransferStatus{}, domain.ErrCapacity
	}
	if len(c.Additions)+len(c.Removals) == 0 && !c.Final {
		return TransferStatus{}, domain.ErrInvalid
	}
	digest := chunkDigest(c)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	entry := s.entries[id]
	if entry == nil || entry.request.InstanceID != instance {
		return TransferStatus{}, domain.ErrNotFound
	}
	if old, ok := entry.chunks[c.Sequence]; ok {
		if old != digest {
			return TransferStatus{}, domain.ErrConflict
		}
		return entry.status, nil
	}
	if entry.status.Final || c.Sequence != entry.status.NextSequence {
		return TransferStatus{}, domain.ErrInvalid
	}
	if entry.request.Mode == domain.Full && len(c.Removals) != 0 {
		return TransferStatus{}, domain.ErrInvalid
	}
	for _, a := range c.Additions {
		if _, ok := entry.sets[a.InboundTag]; !ok {
			return TransferStatus{}, domain.ErrInvalid
		}
	}
	for _, r := range c.Removals {
		if _, ok := entry.sets[r.InboundTag]; !ok {
			return TransferStatus{}, domain.ErrInvalid
		}
	}
	items := entry.items + len(c.Additions) + len(c.Removals)
	if items > entry.expectedItems || size > entry.expectedBytes-entry.bytes {
		return TransferStatus{}, domain.ErrCapacity
	}
	if c.Final && (items != entry.expectedItems || entry.bytes+size != entry.expectedBytes) {
		return TransferStatus{}, domain.ErrInvalid
	}
	for _, a := range c.Additions {
		i := entry.sets[a.InboundTag]
		entry.request.Sets[i].Accounts = append(entry.request.Sets[i].Accounts, bytes.Clone(a.Configuration))
	}
	for _, r := range c.Removals {
		entry.request.Removals = append(entry.request.Removals, accounts.Removal{InboundTag: strings.Clone(r.InboundTag), Email: strings.Clone(r.Email)})
	}
	entry.items, entry.bytes = items, entry.bytes+size
	entry.chunks[c.Sequence] = digest
	entry.status.NextSequence++
	entry.status.Final = c.Final
	return entry.status, nil
}
func (s *Transfers) Finish(ctx context.Context, instance, id string) (domain.Request, error) {
	if err := ctx.Err(); err != nil {
		return domain.Request{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	entry := s.entries[id]
	if entry == nil || entry.request.InstanceID != instance {
		return domain.Request{}, domain.ErrNotFound
	}
	if !entry.status.Final {
		return domain.Request{}, domain.ErrInvalid
	}
	return policies.Own(entry.request), nil
}
func (s *Transfers) Discard(ctx context.Context, instance, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	entry := s.entries[id]
	if entry == nil || entry.request.InstanceID != instance {
		return domain.ErrNotFound
	}
	s.reserved -= entry.reservation
	delete(s.entries, id)
	return nil
}
func (s *Transfers) Close() { s.mu.Lock(); defer s.mu.Unlock(); clear(s.entries); s.reserved = 0 }
