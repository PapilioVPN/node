// Package membership manages live Xray inbound accounts.
package membership

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid         = errors.New("invalid Xray account request")
	ErrInstanceMissing = errors.New("Xray instance not found")
	ErrInboundMissing  = errors.New("Xray inbound not found")
	ErrAccountMissing  = errors.New("Xray account not found")
	ErrProtocol        = errors.New("Xray inbound does not support the requested account protocol")
	ErrExists          = errors.New("Xray account email or authentication identity already exists")
	ErrAmbiguous       = errors.New("Xray inbound has conflicting configured accounts")
	ErrBusy            = errors.New("Xray account operation capacity reached")
	ErrTooLarge        = errors.New("Xray account inventory exceeds the supported limit")
	ErrUnavailable     = errors.New("Xray account operation unavailable")
	ErrUnknownEffect   = errors.New("Xray account mutation outcome unknown")
	ErrFenced          = errors.New("Xray account mutations require a new execution after an unknown outcome")
)

type Scope struct {
	InstanceID string
	InboundTag string
}

type AddRequest struct {
	Scope Scope
	Email string
	UUID  string
}

type RemoveRequest struct {
	Scope Scope
	Email string
}

type UUID [16]byte

func ParseUUID(text string) (UUID, bool) {
	var id UUID
	if len(text) != 36 || text[8] != '-' || text[13] != '-' || text[18] != '-' || text[23] != '-' {
		return id, false
	}
	var raw [32]byte
	offset := copy(raw[:], text[:8])
	offset += copy(raw[offset:], text[9:13])
	offset += copy(raw[offset:], text[14:18])
	offset += copy(raw[offset:], text[19:23])
	copy(raw[offset:], text[24:])
	_, err := hex.Decode(id[:], raw[:])
	return id, err == nil
}

func (id UUID) String() string {
	var result [36]byte
	hex.Encode(result[:8], id[:4])
	hex.Encode(result[9:13], id[4:6])
	hex.Encode(result[14:18], id[6:8])
	hex.Encode(result[19:23], id[8:10])
	hex.Encode(result[24:], id[10:])
	result[8], result[13], result[18], result[23] = '-', '-', '-', '-'
	return string(result[:])
}

type Account struct {
	Email string
	ID    UUID
}

type engine interface {
	Edit(context.Context, string, []string, func(EditSession) error) error
	Add(context.Context, Scope, Account) error
	Remove(context.Context, Scope, string) error
	Count(context.Context, Scope) (int64, error)
}

type Service struct {
	engine  engine
	slots   chan struct{}
	streams chan struct{}
}

func New(engine engine) *Service {
	return &Service{engine: engine, slots: make(chan struct{}, 32), streams: make(chan struct{}, 8)}
}

func (s *Service) Add(ctx context.Context, req AddRequest) error {
	id, valid := ParseUUID(req.UUID)
	if !valid || !validName(req.Email, 1024) {
		return ErrInvalid
	}
	ctx, done, err := s.begin(ctx, req.Scope)
	if err != nil {
		return err
	}
	defer done()
	return s.engine.Add(ctx, req.Scope, Account{Email: req.Email, ID: id})
}

func (s *Service) Remove(ctx context.Context, req RemoveRequest) error {
	if !validName(req.Email, 1024) {
		return ErrInvalid
	}
	ctx, done, err := s.begin(ctx, req.Scope)
	if err != nil {
		return err
	}
	defer done()
	return s.engine.Remove(ctx, req.Scope, req.Email)
}

func (s *Service) Count(ctx context.Context, scope Scope) (int64, error) {
	ctx, done, err := s.begin(ctx, scope)
	if err != nil {
		return 0, err
	}
	defer done()
	return s.engine.Count(ctx, scope)
}

func (s *Service) begin(ctx context.Context, scope Scope) (context.Context, func(), error) {
	return s.beginWithin(ctx, scope, time.Second)
}

func (s *Service) beginWithin(ctx context.Context, scope Scope, timeout time.Duration) (context.Context, func(), error) {
	if !validScope(scope) {
		return nil, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case s.slots <- struct{}{}:
	default:
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, func() {
		cancel()
		<-s.slots
	}, nil
}

func validScope(scope Scope) bool {
	return validName(scope.InstanceID, 63) && validName(scope.InboundTag, 1024)
}

func validName(value string, maxBytes int) bool {
	return len(value) > 0 && len(value) <= maxBytes && utf8.ValidString(value) &&
		strings.IndexFunc(value, unicode.IsControl) == -1
}
