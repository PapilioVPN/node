// Package services selects the configured executable and returns one-request artifacts.
package services

import (
	"context"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

type Generator interface {
	UUID(context.Context, domain.UUIDInput) (domain.UUIDResult, error)
	X25519(context.Context, domain.X25519Input) (domain.CurveResult, error)
	WireGuard(context.Context, domain.WireGuardInput) (domain.CurveResult, error)
	MLDSA65(context.Context, domain.SeedInput) (domain.MLDSA65Result, error)
	MLKEM768(context.Context, domain.SeedInput) (domain.MLKEM768Result, error)
	VLESSEncryption(context.Context) (domain.VLESSResult, error)
	Certificate(context.Context, domain.CertificateInput) (domain.CertificateResult, error)
	CertificateHashes(context.Context, domain.CertificateHashInput) (domain.CertificateHashResult, error)
	ECH(context.Context, domain.ECHInput) (domain.ECHResult, error)
}
type Binding struct {
	ID        string
	Generator Generator
}

// Service selects a configured executable without depending on native API
// readiness. Artifacts exist only in the current response. Repeating random
// generation after a lost response creates new material.
type Service struct{ bindings map[string]Generator }

func New(bindings []Binding) (*Service, error) {
	s := &Service{bindings: make(map[string]Generator, len(bindings))}
	for _, b := range bindings {
		if instances.ID(b.ID) != nil {
			return nil, domain.ErrInvalidArgument
		}
		if _, ok := s.bindings[b.ID]; ok {
			return nil, domain.ErrInvalidArgument
		}
		s.bindings[b.ID] = b.Generator
	}
	return s, nil
}
func (s *Service) selectGenerator(ctx context.Context, id string) (Generator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if instances.ID(id) != nil {
		return nil, domain.ErrInvalidArgument
	}
	generator, exists := s.bindings[id]
	if !exists {
		return nil, domain.ErrNotFound
	}
	if generator == nil {
		return nil, domain.ErrNotConfigured
	}
	return generator, nil
}
func metadata(id, tool string, started time.Time) domain.Metadata {
	return domain.Metadata{InstanceID: id, Tool: "xray." + tool, CompletedAt: time.Now().UTC(), Duration: time.Since(started)}
}

func (s *Service) UUID(ctx context.Context, id string, input domain.UUIDInput) (domain.UUIDResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.UUIDResult{}, err
	}
	if err := validators.UUID(input); err != nil {
		return domain.UUIDResult{}, err
	}
	started := time.Now()
	result, err := generator.UUID(ctx, input)
	if err != nil {
		return domain.UUIDResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.UUIDResult{}, err
	}
	result.Metadata = metadata(id, "uuid", started)
	return result, nil
}

func (s *Service) X25519(ctx context.Context, id string, input domain.X25519Input) (domain.CurveResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.CurveResult{}, err
	}
	input, err = validators.X25519(input)
	if err != nil {
		return domain.CurveResult{}, err
	}
	started := time.Now()
	result, err := generator.X25519(ctx, input)
	if err != nil {
		return domain.CurveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CurveResult{}, err
	}
	result.Metadata = metadata(id, "x25519", started)
	return result, nil
}

func (s *Service) WireGuard(ctx context.Context, id string, input domain.WireGuardInput) (domain.CurveResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.CurveResult{}, err
	}
	if err := validators.WireGuard(input); err != nil {
		return domain.CurveResult{}, err
	}
	started := time.Now()
	result, err := generator.WireGuard(ctx, input)
	if err != nil {
		return domain.CurveResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CurveResult{}, err
	}
	result.Metadata = metadata(id, "wg", started)
	return result, nil
}

func (s *Service) MLDSA65(ctx context.Context, id string, input domain.SeedInput) (domain.MLDSA65Result, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	if err := validators.Seed(input, 32); err != nil {
		return domain.MLDSA65Result{}, err
	}
	started := time.Now()
	result, err := generator.MLDSA65(ctx, input)
	if err != nil {
		return domain.MLDSA65Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.MLDSA65Result{}, err
	}
	result.Metadata = metadata(id, "mldsa65", started)
	return result, nil
}

func (s *Service) MLKEM768(ctx context.Context, id string, input domain.SeedInput) (domain.MLKEM768Result, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	if err := validators.Seed(input, 64); err != nil {
		return domain.MLKEM768Result{}, err
	}
	started := time.Now()
	result, err := generator.MLKEM768(ctx, input)
	if err != nil {
		return domain.MLKEM768Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.MLKEM768Result{}, err
	}
	result.Metadata = metadata(id, "mlkem768", started)
	return result, nil
}

func (s *Service) VLESSEncryption(ctx context.Context, id string) (domain.VLESSResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.VLESSResult{}, err
	}
	started := time.Now()
	result, err := generator.VLESSEncryption(ctx)
	if err != nil {
		return domain.VLESSResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.VLESSResult{}, err
	}
	result.Metadata = metadata(id, "vlessenc", started)
	return result, nil
}

func (s *Service) Certificate(ctx context.Context, id string, input domain.CertificateInput) (domain.CertificateResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.CertificateResult{}, err
	}
	if err := validators.Certificate(input); err != nil {
		return domain.CertificateResult{}, err
	}
	started := time.Now()
	result, err := generator.Certificate(ctx, input)
	if err != nil {
		return domain.CertificateResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CertificateResult{}, err
	}
	result.Metadata = metadata(id, "tls.cert", started)
	return result, nil
}

func (s *Service) CertificateHashes(ctx context.Context, id string, input domain.CertificateHashInput) (domain.CertificateHashResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.CertificateHashResult{}, err
	}
	if len(input.Certificate) == 0 || len(input.Certificate) > domain.MaxCertificateBytes {
		return domain.CertificateHashResult{}, domain.ErrInvalidArgument
	}
	started := time.Now()
	result, err := generator.CertificateHashes(ctx, input)
	if err != nil {
		return domain.CertificateHashResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.CertificateHashResult{}, err
	}
	result.Metadata = metadata(id, "tls.hash", started)
	return result, nil
}

func (s *Service) ECH(ctx context.Context, id string, input domain.ECHInput) (domain.ECHResult, error) {
	generator, err := s.selectGenerator(ctx, id)
	if err != nil {
		return domain.ECHResult{}, err
	}
	input, err = validators.ECH(input)
	if err != nil {
		return domain.ECHResult{}, err
	}
	started := time.Now()
	result, err := generator.ECH(ctx, input)
	if err != nil {
		return domain.ECHResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.ECHResult{}, err
	}
	result.Metadata = metadata(id, "tls.ech", started)
	return result, nil
}
