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
	Scope       Scope
	InboundTags []string
	Email       string
	UUID        *string
	Flow        *string
	Credential  *Credential
	Assignments []Assignment
}

type Outcome string

var (
	ErrRecoveryWrite         = errors.New("Xray account recovery state was not written")
	ErrRecoveryCommitUnknown = errors.New("Xray account recovery state durability is unknown")
)

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

// Key separates the native authentication index from the requested settings.
type Key struct {
	Identity IDKey
	Version  IDKey
}

type PreparedAccount struct {
	Level uint32
	Email string
	Name  string
	Key
	KeepVersion bool
	Value       []byte
}

// PlannedMember describes the accepted membership of a named account after a plan.
type PlannedMember struct {
	Name        string
	Keep        bool
	Replacement *PreparedAccount
}

// EditSession stages accepted membership across all selected inbounds.
type EditSession interface {
	// Inbounds follows the requested tag order. An excluded tag has a nil editor.
	Inbounds() []Editor
	CheckStaged() error
}

// Editor is valid only inside Engine.Edit. Its mutations share one execution
// and reuse the same native effects and acknowledgement fence as Add/Remove.
type Editor interface {
	Name(email string) (string, error)
	Named() iter.Seq2[string, Key]
	Capacity() int
	Lookup(name string) (Key, bool)
	HasID(IDKey) bool
	Prepare(email string, credential Credential) (PreparedAccount, error)
	Stage(full bool, members []PlannedMember, removals []string, additions []PreparedAccount) error
	Add(PreparedAccount) error
	Remove(name string) error
}

func (req SyncUserRequest) account() AccountInput {
	return AccountInput{
		Email: req.Email, UUID: req.UUID, Flow: req.Flow, Credential: req.Credential, Assignments: req.Assignments,
	}
}

func (s *Service) SyncUser(ctx context.Context, req SyncUserRequest) (SyncResult, error) {
	return s.reconcile(ctx, req.Scope, req.InboundTags, time.Second, []AccountInput{req.account()}, false)
}

func (s *Service) reconcile(ctx context.Context, scope Scope, tags []string, timeout time.Duration, accounts []AccountInput, full bool) (SyncResult, error) {
	selected, err := selectInbounds(scope, tags)
	if err != nil {
		return SyncResult{}, err
	}
	groups, err := selected.changes(accounts, full)
	if err != nil {
		return SyncResult{}, err
	}

	var result SyncResult
	ctx, done, err := s.beginWithin(ctx, Scope{InstanceID: selected.instanceID, InboundTag: selected.tags[0]}, timeout)
	if err == nil {
		defer done()
		err = s.engine.Edit(ctx, selected.instanceID, selected.tags, func(session EditSession) error {
			return applyPlans(session, groups, full, &result)
		})
	}
	if errors.Is(err, ErrInvalid) {
		return SyncResult{}, err
	}
	switch {
	case err == nil:
		result.Outcome = Confirmed
	case errors.Is(err, ErrUnknownEffect), errors.Is(err, ErrRecoveryCommitUnknown):
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

func (r *SyncResult) add(editor Editor, account PreparedAccount) error {
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
	case errors.Is(err, ErrRecoveryWrite):
		return "recovery_write_failed"
	case errors.Is(err, ErrRecoveryCommitUnknown):
		return "recovery_commit_unknown"
	default:
		return "unavailable"
	}
}
