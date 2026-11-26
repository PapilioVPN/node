package process

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/hysteria/configs/preflight"
	lifecycle "github.com/PapilioVPN/node/internal/engines/hysteria/instances/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
)

type Backend struct {
	Executable, ConfigFile, RuntimeDirectory string
}

type candidate struct {
	backend  Backend
	source   []byte
	prepared preflight.Prepared
}

func (b Backend) Source(ctx context.Context, source []byte) ([]byte, error) {
	return (preflight.Source{Path: b.ConfigFile}).Read(ctx, source)
}

func (b Backend) Prepare(ctx context.Context, source []byte) (lifecycle.Candidate, error) {
	prepared, issue, err := (preflight.Source{Path: b.ConfigFile}).Check(ctx, source)
	if err != nil {
		return nil, err
	}
	if issue != "" {
		return nil, fault.ErrInvalid
	}
	return &candidate{backend: b, source: source, prepared: prepared}, nil
}
