package membership

import (
	"context"
	"crypto/sha256"
	"errors"
	"iter"
	"strings"
	"time"
)

type SyncUserRequest struct {
	Scope Scope
	Email string
	UUID  *string // Absent means the named account should be absent.
}

type Outcome string

const (
	Confirmed  Outcome = "confirmed"
	Partial    Outcome = "partial"
	Unknown    Outcome = "unknown"
	NotApplied Outcome = "not_applied"
)

type SyncResult struct {
	Outcome Outcome `json:"outcome"`
	Added   uint32  `json:"added"`
	Removed uint32  `json:"removed"`
	Failure string  `json:"failure,omitempty"`
}

// IDKey compares VLESS authentication identities without retaining credentials.
type IDKey [sha256.Size]byte

func (id UUID) Key() IDKey {
	// Xray blanks these bytes for lookup and deletion.
	id[6], id[7] = 0, 0
	return sha256.Sum256(id[:])
}

func NameKey(email string) string { return strings.ToLower(email) }

// Editor is valid only inside Engine.Edit. Its mutations share one execution
// and reuse the same native effects and acknowledgement fence as Add/Remove.
type Editor interface {
	Named() iter.Seq2[string, IDKey]
	Capacity() int
	Lookup(email string) (IDKey, bool)
	HasID(IDKey) bool
	Add(Account) error
	Remove(email string) error
}

func (s *Service) SyncUser(ctx context.Context, req SyncUserRequest) (SyncResult, error) {
	if !validName(req.Email, 1024) {
		return SyncResult{}, ErrInvalid
	}
	var id UUID
	if req.UUID != nil {
		var valid bool
		id, valid = ParseUUID(*req.UUID)
		if !valid {
			return SyncResult{}, ErrInvalid
		}
	}
	return s.sync(ctx, req.Scope, time.Second, func(editor Editor, result *SyncResult) error {
		current, exists := editor.Lookup(req.Email)
		if req.UUID != nil {
			key := id.Key()
			if exists && current == key {
				return nil
			}
			if editor.HasID(key) {
				return ErrExists
			}
		}
		if exists {
			if err := result.remove(editor, req.Email); err != nil {
				return err
			}
		}
		if req.UUID != nil {
			return result.add(editor, Account{Email: req.Email, ID: id})
		}
		return nil
	})
}

func (s *Service) sync(ctx context.Context, scope Scope, timeout time.Duration, apply func(Editor, *SyncResult) error) (SyncResult, error) {
	var result SyncResult
	ctx, done, err := s.beginWithin(ctx, scope, timeout)
	if errors.Is(err, ErrInvalid) {
		return result, err
	}
	if err == nil {
		defer done()
		err = s.engine.Edit(ctx, scope, func(editor Editor) error { return apply(editor, &result) })
	}
	switch {
	case err == nil:
		result.Outcome = Confirmed
	case errors.Is(err, ErrUnknownEffect):
		result.Outcome = Unknown
	case result.Added+result.Removed > 0:
		result.Outcome = Partial
	default:
		result.Outcome = NotApplied
	}
	if err != nil {
		result.Failure = syncFailure(err)
	}
	return result, nil
}

func (r *SyncResult) add(editor Editor, account Account) error {
	if err := editor.Add(account); err != nil {
		return err
	}
	r.Added++
	return nil
}

func (r *SyncResult) remove(editor Editor, email string) error {
	if err := editor.Remove(email); err != nil {
		return err
	}
	r.Removed++
	return nil
}

func syncFailure(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, ErrInstanceMissing):
		return "instance_missing"
	case errors.Is(err, ErrInboundMissing):
		return "inbound_missing"
	case errors.Is(err, ErrProtocol):
		return "wrong_protocol"
	case errors.Is(err, ErrAmbiguous):
		return "ambiguous"
	case errors.Is(err, ErrExists):
		return "conflict"
	case errors.Is(err, ErrBusy):
		return "busy"
	case errors.Is(err, ErrFenced):
		return "fenced"
	case errors.Is(err, ErrTooLarge):
		return "capacity"
	case errors.Is(err, ErrUnknownEffect):
		return "native_acknowledgement_unknown"
	default:
		return "unavailable"
	}
}
