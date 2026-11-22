package services

import (
	"context"
	"crypto/rand"
	"math"
	"strings"
	"sync"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
)

type Observer interface {
	Snapshot(context.Context, string, []string) (domain.Snapshot, error)
}
type Accounts interface {
	AddMany(context.Context, string, []accounts.Addition, int) (accounts.Batch, error)
	RemoveMany(context.Context, string, []accounts.Removal, int) (accounts.Batch, error)
}
type syncEntry struct {
	busy    bool
	control uint64
	plan    *domain.Plan
	digest  [32]byte
	full    ports.ReviewV1
	receipt *domain.Receipt
	cause   error
	bytes   int
}

// Service retains one review/receipt per configured instance. Pending and
// retained reviews/receipts share eight slots. Retained data shares
// 32 MiB logical storage; pending input/native decoding is additional memory.
// Completed receipts become reclaimable after 30 seconds; uncertain receipts
// remain until explicit resolution. Native I/O holds no state lock;
// competing syncs reject rather than queue. Other Accounts writers still own
// their separate admission: preview/read/check/write is not native atomicity.
type Service struct {
	mu              sync.Mutex
	entries         map[string]*syncEntry
	epoch           string
	observer        Observer
	accounts        Accounts
	configs         ports.ConfigsV1
	decoder         policies.Decoder
	retained, bytes int
	pending         int
	now             func() time.Time
}

func New(ids []string, observer Observer, accounts Accounts, configs ports.ConfigsV1, decoder policies.Decoder) (*Service, error) {
	if observer == nil || accounts == nil || configs == nil || decoder == nil || len(ids) > 128 {
		return nil, domain.ErrInvalid
	}
	s := &Service{entries: make(map[string]*syncEntry), epoch: rand.Text(), observer: observer, accounts: accounts, configs: configs, decoder: decoder, now: time.Now}
	for _, id := range ids {
		if !policies.Identifier(id) || s.entries[id] != nil {
			return nil, domain.ErrInvalid
		}
		s.entries[strings.Clone(id)] = &syncEntry{}
	}
	return s, nil
}
func (s *Service) acquire(ctx context.Context, id string) (*syncEntry, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !policies.Identifier(id) {
		return nil, nil, domain.ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.entries[id]
	if e == nil {
		return nil, nil, domain.ErrNotFound
	}
	if e.busy {
		return nil, nil, domain.ErrCapacity
	}
	e.busy = true
	return e, func() { s.mu.Lock(); e.busy = false; s.mu.Unlock() }, nil
}
func (s *Service) Revision(ctx context.Context, id string) (domain.Revision, error) {
	e, release, err := s.acquire(ctx, id)
	if err != nil {
		return domain.Revision{}, err
	}
	defer release()
	snapshot, err := s.observer.Snapshot(ctx, id, nil)
	if err != nil {
		return domain.Revision{}, err
	}
	snapshot.Revision.Epoch, snapshot.Revision.Control = s.epoch, e.control
	return snapshot.Revision, nil
}
func planBytes(p domain.Plan) int {
	n := 1024 + len(p.Request.InstanceID) + len(p.Request.OperationID) + len(p.Request.Expected.Epoch)
	for _, tag := range p.Request.Selection {
		n += len(tag) + 32
	}
	for _, r := range p.Request.Removals {
		n += len(r.InboundTag) + len(r.Email) + 64
	}
	for _, set := range p.Request.Sets {
		n += len(set.InboundTag) + 64
		for _, raw := range set.Accounts {
			n += len(raw) + 32
		}
	}
	for _, in := range p.Snapshot.Inbounds {
		n += len(in.Tag) + len(in.AccountType) + len(in.EmailMatching) + 64
		for _, u := range in.Users {
			n += len(u.Email) + len(u.AccountType) + len(u.Configuration) + 128
		}
	}
	for _, step := range p.Steps {
		n += len(step.InboundTag) + len(step.Email) + len(step.Configuration) + 256
	}
	return n
}
func clonePreview(p domain.Preview) domain.Preview {
	p.Differences = append([]domain.Difference(nil), p.Differences...)
	return p
}

func (s *Service) reclaim() {
	for _, e := range s.entries {
		if e.busy || e.plan == nil {
			continue
		}
		expired := e.receipt == nil && !s.now().Before(e.plan.Preview.ExpiresAt)
		if e.receipt != nil && !e.receipt.RequiresRecovery && !s.now().Before(e.receipt.CompletedAt.Add(domain.Lifetime)) {
			expired = true
		}
		if expired {
			s.retained--
			s.bytes -= e.bytes
			e.plan = nil
			e.receipt = nil
			e.cause = nil
			e.full = ports.ReviewV1{}
			e.bytes = 0
		}
	}
}

func (s *Service) Preview(ctx context.Context, r domain.Request) (domain.Preview, error) {
	if err := policies.Validate(ctx, r); err != nil {
		return domain.Preview{}, err
	}
	e, release, err := s.acquire(ctx, r.InstanceID)
	if err != nil {
		return domain.Preview{}, err
	}
	defer release()
	digest := policies.Digest(r)
	s.mu.Lock()
	if e.plan != nil && e.plan.Request.OperationID == r.OperationID {
		if e.digest != digest {
			s.mu.Unlock()
			return domain.Preview{}, domain.ErrConflict
		}
		if e.receipt != nil || s.now().Before(e.plan.Preview.ExpiresAt) {
			result := clonePreview(e.plan.Preview)
			s.mu.Unlock()
			return result, nil
		}
	}
	uncertain := e.receipt != nil && e.receipt.RequiresRecovery
	s.mu.Unlock()
	if uncertain {
		return domain.Preview{}, domain.ErrUncertain
	}
	if r.Expected.Epoch != s.epoch || r.Expected.Control != e.control || e.control == math.MaxUint64 {
		return domain.Preview{}, domain.ErrStale
	}
	s.mu.Lock()
	s.reclaim()
	occupied := s.retained + s.pending
	if e.plan != nil {
		occupied--
	}
	if occupied >= domain.MaxTransfers {
		s.mu.Unlock()
		return domain.Preview{}, domain.ErrCapacity
	}
	s.pending++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.pending--; s.mu.Unlock() }()
	r = policies.Own(r)
	snapshot, err := s.observer.Snapshot(ctx, r.InstanceID, r.Selection)
	if err != nil {
		return domain.Preview{}, err
	}
	snapshot.Revision.Epoch, snapshot.Revision.Control = s.epoch, e.control
	p, err := policies.Build(ctx, r, snapshot, s.decoder)
	if err != nil {
		return domain.Preview{}, err
	}
	full := ports.ReviewV1{}
	if r.Mode == domain.Full {
		full, err = s.configs.PlanMembership(ctx, ports.MembershipV1{InstanceID: r.InstanceID, OperationID: r.OperationID, ExpectedRevision: r.Expected.Config, Generation: r.Expected.Generation, Sets: r.Sets})
		if err != nil {
			return domain.Preview{}, err
		}
		if full.Token == "" {
			return domain.Preview{}, domain.ErrUnsupported
		}
		p.Preview.ApplyMode, p.Preview.Reason = full.Mode, full.Reason
		p.Preview.ConfigOperationID = full.Token
	}
	p.Preview.PlanID, p.Preview.ExpiresAt = rand.Text(), s.now().UTC().Add(domain.Lifetime)
	if err := ctx.Err(); err != nil {
		return domain.Preview{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	size := planBytes(p)
	if s.bytes-e.bytes+size > domain.MaxRetainedBytes || e.plan == nil && s.retained >= domain.MaxTransfers {
		return domain.Preview{}, domain.ErrCapacity
	}
	if e.plan == nil {
		s.retained++
	}
	s.bytes += size - e.bytes
	e.plan, e.digest, e.full, e.receipt, e.cause, e.bytes = &p, digest, full, nil, nil, size
	return clonePreview(p.Preview), nil
}

// Status pages immutable local evidence by operation identity. Offset zero also
// returns metadata while execution is in flight. A later operation supersedes it.
func (s *Service) Status(ctx context.Context, id, operation string, offset int) (domain.ReceiptPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReceiptPage{}, err
	}
	if !policies.Identifier(id) || !policies.Identifier(operation) || len(operation) > 128 || offset < 0 {
		return domain.ReceiptPage{}, domain.ErrInvalid
	}
	s.mu.Lock()
	e := s.entries[id]
	if e == nil {
		s.mu.Unlock()
		return domain.ReceiptPage{}, domain.ErrNotFound
	}
	if e.receipt == nil || e.receipt.OperationID != operation {
		s.mu.Unlock()
		if offset != 0 {
			return domain.ReceiptPage{}, domain.ErrInvalid
		}
		result, err := s.configs.MembershipStatus(ctx, id, operation)
		if err != nil {
			return domain.ReceiptPage{}, err
		}
		r := domain.Receipt{InstanceID: id, OperationID: operation, ConfigOperationID: operation, Revision: domain.Revision{Epoch: s.epoch}}
		applyFullResult(&r, result, nil)
		return domain.ReceiptPage{Receipt: r}, nil
	}
	defer s.mu.Unlock()
	r := *e.receipt
	if offset < 0 || offset > len(r.Steps) {
		return domain.ReceiptPage{}, domain.ErrInvalid
	}
	end := min(offset+50, len(r.Steps))
	r.Steps = append([]domain.StepResult{}, r.Steps[offset:end]...)
	page := domain.ReceiptPage{Receipt: r}
	if end < len(e.receipt.Steps) {
		page.NextOffset = end
	}
	return page, nil
}
