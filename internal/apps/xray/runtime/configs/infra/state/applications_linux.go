//go:build linux

package state

import (
	"context"
	"encoding/json"
	"math"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func document(id string, desired domain.Desired) record {
	row := record{Schema: 1, InstanceID: id, Revision: desired.Metadata.Revision, SHA256: sourceDigest(desired.Sources), UpdatedAt: desired.Metadata.UpdatedAt, Sources: make([]sourceRecord, len(desired.Sources))}
	for i, source := range desired.Sources {
		row.Sources[i] = sourceRecord{Name: source.Name, Content: source.Content}
	}
	return row
}

// WriteReviewed publishes original candidate/previous sources and the consumed
// identity together. Every new acceptance advances the revision, including a
// native no-op; replay of that identity does not publish another revision.
func (s *Store) WriteReviewed(ctx context.Context, expected uint64, sources []domain.Source, previous domain.Desired, application domain.Application) (domain.Persistence, error) {
	if err := validators.Sources(sources); err != nil {
		return domain.Persistence{}, err
	}
	if err := validators.Sources(previous.Sources); err != nil {
		return domain.Persistence{}, err
	}
	if !validIdentity(application.ID) || application.InstanceID != s.id || previous.Metadata.Revision != expected {
		return domain.Persistence{}, domain.ErrInvalidArgument
	}
	root, release, err := s.acquire(ctx)
	if err != nil {
		return domain.Persistence{}, err
	}
	defer release()
	s.mu.RLock()
	metadata, uncertain := s.metadata, s.uncertain
	s.mu.RUnlock()
	if uncertain {
		return domain.Persistence{}, domain.ErrDurabilityUnknown
	}
	if metadata.Revision != expected {
		return domain.Persistence{}, domain.ErrConflict
	}
	if expected == math.MaxUint64 {
		return domain.Persistence{}, domain.ErrCapacity
	}
	if expected != 0 && metadata.SHA256 != sourceDigest(previous.Sources) {
		return domain.Persistence{}, domain.ErrConflict
	}
	next := domain.Desired{Metadata: domain.DesiredMetadata{Revision: expected + 1, SHA256: sourceDigest(sources), UpdatedAt: time.Now().UTC(), SourceCount: len(sources)}, Sources: sources}
	application.ExpectedRevision = expected
	application.Revision = next.Metadata.Revision
	application.PreviousRevision = expected
	application.CandidateSHA256 = next.Metadata.SHA256
	row := document(s.id, next)
	row.Schema = 2
	prior := document(s.id, previous)
	row.Previous = &prior
	row.Application = &application
	data, err := json.Marshal(row)
	if err != nil {
		return domain.Persistence{}, err
	}
	return s.publishDocument(ctx, root, next.Metadata, data)
}

func validIdentity(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, b := range []byte(id) {
		if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
			return false
		}
	}
	return true
}

func (s *Store) ReadApplication(ctx context.Context) (domain.StoredApplication, error) {
	root, release, err := s.acquire(ctx)
	if err != nil {
		return domain.StoredApplication{}, err
	}
	defer release()
	row, desired, err := readDocument(root, s.id)
	if err != nil {
		return domain.StoredApplication{}, err
	}
	if row.Application == nil || row.Previous == nil {
		return domain.StoredApplication{}, domain.ErrNoApplication
	}
	if err := ctx.Err(); err != nil {
		return domain.StoredApplication{}, err
	}
	previous, err := decodeSources(*row.Previous)
	if err != nil {
		return domain.StoredApplication{}, err
	}
	application := *row.Application
	s.mu.RLock()
	if !s.uncertain && (s.metadata.Revision != desired.Metadata.Revision || s.metadata.SHA256 != desired.Metadata.SHA256) {
		s.mu.RUnlock()
		return domain.StoredApplication{}, domain.ErrConflict
	}
	application.ReceiptDurable = s.metadata.Durable && !s.uncertain && s.metadata.Revision == desired.Metadata.Revision
	s.mu.RUnlock()
	return domain.StoredApplication{Application: application, Previous: previous}, nil
}

// SaveApplication updates only evidence for the current accepted identity. The
// same atomic record retains the candidate and original previous sources.
func (s *Store) SaveApplication(ctx context.Context, expected uint64, id string, application domain.Application) error {
	if !validIdentity(id) || application.ID != id || application.InstanceID != s.id {
		return domain.ErrInvalidArgument
	}
	root, release, err := s.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	row, desired, err := readDocument(root, s.id)
	if err != nil {
		return err
	}
	if desired.Metadata.Revision != expected || row.Application == nil || row.Application.ID != id {
		return domain.ErrConflict
	}
	row.Application = &application
	data, err := json.Marshal(row)
	if err != nil {
		return err
	}
	_, err = s.publishDocument(ctx, root, desired.Metadata, data)
	return err
}

// WriteRecovery consumes a controller-supplied recovery identity and republishes
// the original previous collection at a NEW revision. It never rewinds the CAS
// counter or overwrites the previous sources with a failed candidate.
func (s *Store) WriteRecovery(ctx context.Context, expected uint64, applicationID, recoveryID string) (domain.StoredApplication, domain.Persistence, error) {
	if !validIdentity(applicationID) || !validIdentity(recoveryID) {
		return domain.StoredApplication{}, domain.Persistence{}, domain.ErrInvalidArgument
	}
	root, release, err := s.acquire(ctx)
	if err != nil {
		return domain.StoredApplication{}, domain.Persistence{}, err
	}
	defer release()
	row, current, err := readDocument(root, s.id)
	if err != nil {
		return domain.StoredApplication{}, domain.Persistence{}, err
	}
	if current.Metadata.Revision != expected {
		return domain.StoredApplication{}, domain.Persistence{}, domain.ErrConflict
	}
	if row.Application == nil || row.Application.ID != applicationID || row.Previous == nil {
		return domain.StoredApplication{}, domain.Persistence{}, domain.ErrNoApplication
	}
	application := *row.Application
	previous, err := decodeSources(*row.Previous)
	if err != nil {
		return domain.StoredApplication{}, domain.Persistence{}, err
	}
	if application.Recovery != nil {
		return domain.StoredApplication{Application: application, Previous: previous}, domain.Persistence{}, domain.ErrConflict
	}
	if !application.RequiresRecovery || expected == math.MaxUint64 {
		return domain.StoredApplication{}, domain.Persistence{}, domain.ErrConflict
	}
	next := domain.DesiredMetadata{Revision: expected + 1, SHA256: previous.Metadata.SHA256, SourceCount: len(previous.Sources), UpdatedAt: time.Now().UTC()}
	application.Recovery = &domain.Recovery{ID: recoveryID, ExpectedRevision: expected, Revision: next.Revision, Outcome: "in_progress", EngineEffect: "not_attempted", Readiness: "not_checked", StartedAt: time.Now().UTC()}
	row.Revision, row.SHA256, row.UpdatedAt, row.Sources = next.Revision, next.SHA256, next.UpdatedAt, row.Previous.Sources
	row.Application = &application
	data, err := json.Marshal(row)
	if err != nil {
		return domain.StoredApplication{}, domain.Persistence{}, err
	}
	result, err := s.publishDocument(ctx, root, next, data)
	return domain.StoredApplication{Application: application, Previous: previous}, result, err
}
