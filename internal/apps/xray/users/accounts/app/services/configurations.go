package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type ConfigurationCompiler interface {
	Compile(context.Context, string, []configs.Source) (configs.Compilation, error)
}
type MembershipBuilder func(context.Context, []byte, []domain.InboundUserSet) ([]byte, error)

// Configurations prepares artifacts, independently of live account admission or
// engine readiness. Two complete operations bound compilation plus replacement.
type Configurations struct {
	compiler ConfigurationCompiler
	build    MembershipBuilder
	gate     chan struct{}
}

func NewConfigurations(compiler ConfigurationCompiler, build MembershipBuilder) (*Configurations, error) {
	if compiler == nil || build == nil {
		return nil, domain.ErrInvalidArgument
	}
	return &Configurations{compiler: compiler, build: build, gate: make(chan struct{}, 2)}, nil
}

func (s *Configurations) Prepare(ctx context.Context, id string, sources []configs.Source, sets []domain.InboundUserSet) (configs.Compilation, error) {
	if err := ctx.Err(); err != nil {
		return configs.Compilation{}, err
	}
	if err := validators.UserSets(ctx, sets); err != nil {
		return configs.Compilation{}, err
	}
	select {
	case s.gate <- struct{}{}:
	default:
		return configs.Compilation{}, domain.ErrCapacity
	}
	defer func() { <-s.gate }()
	started := time.Now()
	compiled, err := s.compiler.Compile(ctx, id, sources)
	if err != nil {
		return configs.Compilation{}, configurationError(err)
	}
	content, err := s.build(ctx, compiled.Content, sets)
	if err != nil {
		return configs.Compilation{}, err
	}
	if err := ctx.Err(); err != nil {
		return configs.Compilation{}, err
	}
	hash := sha256.Sum256(content)
	return configs.Compilation{InstanceID: id, Content: content, SHA256: hex.EncodeToString(hash[:]), PreparedAt: time.Now().UTC(), Duration: time.Since(started)}, nil
}

func configurationError(err error) error {
	switch {
	case errors.Is(err, configs.ErrCapacity), errors.Is(err, configs.ErrTooLarge):
		return errors.Join(domain.ErrCapacity, err)
	case errors.Is(err, configs.ErrNotFound):
		return errors.Join(domain.ErrNotFound, err)
	case errors.Is(err, configs.ErrUnsupported), errors.Is(err, configs.ErrClosed):
		return errors.Join(domain.ErrUnsupported, err)
	case errors.Is(err, configs.ErrInvalidArgument), errors.Is(err, configs.ErrRejected):
		return errors.Join(domain.ErrInvalidArgument, err)
	default:
		return err
	}
}
