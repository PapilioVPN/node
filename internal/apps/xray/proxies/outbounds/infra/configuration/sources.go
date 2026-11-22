// Package configuration prepares one selected native outbound from source files.
package configuration

import (
	"context"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/domain"
	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

// Compiler borrows the existing configuration owner's bounded native converter.
type Compiler interface {
	Compile(context.Context, string, []configs.Source) (configs.Compilation, error)
}
type Preparer struct{ Compiler Compiler }

// Prepare selects exactly one matching tag. Other handlers, features and source
// environment settings are not applied to the running execution.
func (p Preparer) Prepare(ctx context.Context, id, tag string, sources []configs.Source) ([]byte, error) {
	compiled, err := p.Compiler.Compile(ctx, id, sources)
	if err != nil {
		return nil, compileError(err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(compiled.Content) > configs.MaxCompiledBytes {
		return nil, domain.ErrCapacity
	}
	var configuration core.Config
	if err := proto.Unmarshal(compiled.Content, &configuration); err != nil {
		return nil, errors.Join(domain.ErrInvalidArgument, err)
	}
	var selected *core.OutboundHandlerConfig
	for _, outbound := range configuration.Outbound {
		if outbound != nil && outbound.Tag == tag {
			if selected != nil {
				return nil, domain.ErrInvalidArgument
			}
			selected = outbound
		}
	}
	if selected == nil {
		return nil, domain.ErrInvalidArgument
	}
	if proto.Size(selected) > domain.MaxConfigurationBytes {
		return nil, domain.ErrCapacity
	}
	content, err := proto.Marshal(selected)
	if err != nil {
		return nil, err
	}
	return content, ctx.Err()
}
func compileError(err error) error {
	switch {
	case errors.Is(err, configs.ErrCapacity), errors.Is(err, configs.ErrTooLarge):
		return errors.Join(domain.ErrCapacity, err)
	case errors.Is(err, configs.ErrNotFound):
		return errors.Join(domain.ErrNotFound, err)
	case errors.Is(err, configs.ErrUnsupported), errors.Is(err, configs.ErrClosed):
		return errors.Join(domain.ErrUnsupported, err)
	case errors.Is(err, configs.ErrInvalidArgument), errors.Is(err, configs.ErrRejected):
		return errors.Join(domain.ErrInvalidArgument, err)
	}
	return err
}
