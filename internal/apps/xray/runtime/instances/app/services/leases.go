package services

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"math"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

// LeaseStore is private instance state, never a controller/user database. Callers
// serialize it with execution mutations. Tokens are returned only on acquisition.
type LeaseStore interface {
	LoadLease(context.Context) (domain.LeaseRecord, error)
	SaveLease(context.Context, domain.LeaseRecord) error
}

func (s *Service) leaseEntry(ctx context.Context, id string) (*instance, func(), error) {
	e, err := s.selectInstance(ctx, id)
	if err != nil {
		return nil, func() {}, err
	}
	if e.binding.Leases == nil {
		return nil, func() {}, domain.ErrUnsupported
	}
	select {
	case e.mutation <- struct{}{}:
	default:
		return nil, func() {}, domain.ErrBusy
	}
	release := func() { <-e.mutation }
	if s.closed.Load() {
		release()
		return nil, func() {}, domain.ErrClosed
	}
	return e, release, nil
}
func loadLease(ctx context.Context, e *instance) (domain.LeaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.LeaseRecord{}, err
	}
	if e.leaseDisabled {
		return domain.LeaseRecord{}, domain.ErrUnsupported
	}
	if e.lease != nil {
		return *e.lease, nil
	}
	r, err := e.binding.Leases.LoadLease(ctx)
	if errors.Is(err, domain.ErrUnsupported) {
		e.leaseDisabled = true
	}
	if err != nil {
		return r, err
	}
	if r.Status.Revision == 0 {
		r.Status.State = "none"
		r.Status.StopEffect = domain.NotApplied
		e.lease = &r
		return r, nil
	}
	if r.Status.Revision > math.MaxUint64-2 || len(r.Token) != 64 || len(r.Status.Owner) > 128 || len(r.Status.ExecutionID) != 32 || r.Status.ExpiresAt == nil || (r.Status.Policy != "stop" && r.Status.Policy != "keep_running") {
		return r, domain.ErrOwnership
	}
	switch r.Status.State {
	case "active", "expiry_pending", "expired", "released":
	default:
		return r, domain.ErrOwnership
	}
	e.lease = &r
	return r, nil
}
func leaseStatus(r domain.LeaseRecord) domain.LeaseStatus {
	v := r.Status
	if v.ExpiresAt != nil {
		value := *v.ExpiresAt
		v.ExpiresAt = &value
	}
	v.ObservedAt = time.Now().UTC()
	return v
}
func (s *Service) Lease(ctx context.Context, id string) (domain.LeaseStatus, error) {
	e, release, err := s.leaseEntry(ctx, id)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	defer release()
	r, err := loadLease(ctx, e)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	// Report an elapsed deadline even when the bounded expiry action is pending.
	v := leaseStatus(r)
	if v.State == "active" && !time.Now().Before(*v.ExpiresAt) {
		v.State = "expiry_pending"
	}
	return v, nil
}
func (s *Service) AcquireLease(ctx context.Context, id string, q domain.AcquireLease) (domain.LeaseReceipt, error) {
	if err := validators.AcquireLease(q); err != nil {
		return domain.LeaseReceipt{}, err
	}
	e, release, err := s.leaseEntry(ctx, id)
	if err != nil {
		return domain.LeaseReceipt{}, err
	}
	defer release()
	r, err := loadLease(ctx, e)
	if err != nil {
		return domain.LeaseReceipt{}, err
	}
	if r.Status.Revision != q.ExpectedRevision || r.Status.State == "active" || r.Status.State == "expiry_pending" {
		return domain.LeaseReceipt{}, domain.ErrConflict
	}
	current := e.binding.Runtime.Observe()
	if current.State != domain.Running || current.ExecutionID != q.ExecutionID {
		return domain.LeaseReceipt{}, domain.ErrConflict
	}
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return domain.LeaseReceipt{}, err
	}
	expires := time.Now().UTC().Add(time.Duration(q.TTLSeconds) * time.Second)
	r = domain.LeaseRecord{Token: hex.EncodeToString(token[:]), Status: domain.LeaseStatus{Revision: r.Status.Revision + 1, State: "active", Owner: q.Owner, ExecutionID: q.ExecutionID, Policy: q.Policy, ExpiresAt: &expires, StopEffect: domain.NotApplied}}
	if err := saveLease(ctx, e, r); err != nil {
		return domain.LeaseReceipt{}, err
	}
	return domain.LeaseReceipt{Status: leaseStatus(r), Token: r.Token}, nil
}
func tokenMatches(a, b string) bool {
	return len(b) == 64 && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Service) RenewLease(ctx context.Context, id string, q domain.RenewLease) (domain.LeaseStatus, error) {
	if !validators.TTL(q.TTLSeconds) || len(q.Token) != 64 {
		return domain.LeaseStatus{}, domain.ErrInvalidArgument
	}
	e, release, err := s.leaseEntry(ctx, id)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	defer release()
	r, err := loadLease(ctx, e)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	current := e.binding.Runtime.Observe()
	if r.Status.Revision != q.ExpectedRevision || r.Status.State != "active" || !time.Now().Before(*r.Status.ExpiresAt) || !tokenMatches(r.Token, q.Token) || current.State != domain.Running || current.ExecutionID != r.Status.ExecutionID {
		return domain.LeaseStatus{}, domain.ErrConflict
	}
	expires := time.Now().UTC().Add(time.Duration(q.TTLSeconds) * time.Second)
	r.Status.ExpiresAt = &expires
	r.Status.Revision++
	if err := saveLease(ctx, e, r); err != nil {
		return domain.LeaseStatus{}, err
	}
	return leaseStatus(r), nil
}
func (s *Service) ReleaseLease(ctx context.Context, id string, q domain.ReleaseLease) (domain.LeaseStatus, error) {
	if len(q.Token) != 64 {
		return domain.LeaseStatus{}, domain.ErrInvalidArgument
	}
	e, release, err := s.leaseEntry(ctx, id)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	defer release()
	r, err := loadLease(ctx, e)
	if err != nil {
		return domain.LeaseStatus{}, err
	}
	if r.Status.Revision != q.ExpectedRevision || r.Status.State != "active" || !time.Now().Before(*r.Status.ExpiresAt) || !tokenMatches(r.Token, q.Token) {
		return domain.LeaseStatus{}, domain.ErrConflict
	}
	r.Status.State = "released"
	r.Status.Revision++
	if err := saveLease(ctx, e, r); err != nil {
		return domain.LeaseStatus{}, err
	}
	return leaseStatus(r), nil
}

func (s *Service) expireLease(ctx context.Context, e *instance) error {
	if e.binding.Leases == nil {
		return nil
	}
	select {
	case e.mutation <- struct{}{}:
	default:
		return domain.ErrBusy
	}
	defer func() { <-e.mutation }()
	r, err := loadLease(ctx, e)
	if errors.Is(err, domain.ErrUnsupported) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Status.State != "expiry_pending" && (r.Status.State != "active" || time.Now().Before(*r.Status.ExpiresAt)) {
		return nil
	}
	if r.Status.State == "active" {
		r.Status.State = "expiry_pending"
		r.Status.Revision++
		if err := saveLease(ctx, e, r); err != nil {
			return err
		}
	}
	current := e.binding.Runtime.Observe()
	if r.Status.Policy == "stop" && current.ExecutionID == r.Status.ExecutionID && current.State == domain.Running {
		r.Status.StopEffect = domain.Unknown
		if err := e.binding.Runtime.Stop(ctx); err == nil {
			r.Status.StopEffect = domain.Completed
		} else {
			r.Status.Error = "stop_failed"
		}
	} else if current.ExecutionID != r.Status.ExecutionID {
		r.Status.Error = "execution_changed"
	}
	r.Status.State = "expired"
	r.Status.Revision++
	return saveLease(ctx, e, r)
}

// StartLeases restores due actions before listeners start. No lease is created
// implicitly by reads, readiness or start. Each configured instance has one
// joined worker, so a slow stop cannot delay another instance's expiry.
func (s *Service) StartLeases(ctx context.Context) error {
	for _, e := range s.ordered {
		if err := s.expireLease(ctx, e); err != nil {
			return err
		}
	}
	s.leaseOnce.Do(func() {
		for _, e := range s.ordered {
			if e.binding.Leases == nil || e.leaseDisabled {
				continue
			}
			s.leaseWorkers.Add(1)
			go func() {
				defer s.leaseWorkers.Done()
				ticker := time.NewTicker(50 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-s.lifetime.Done():
						return
					case <-ticker.C:
						action, cancel := context.WithTimeout(s.lifetime, 2*time.Second)
						_ = s.expireLease(action, e)
						cancel()
					}
				}
			}()
		}
	})
	return nil
}

func saveLease(ctx context.Context, e *instance, r domain.LeaseRecord) error {
	if err := e.binding.Leases.SaveLease(ctx, r); err != nil {
		e.lease = nil
		return err
	}
	e.lease = &r
	return nil
}
