// Package configuration selects the native router payload after one compilation.
package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	configdomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

type Compiler interface {
	Compile(context.Context, string, []configdomain.Source) (configdomain.Compilation, error)
}
type Preparer struct{ Compiler Compiler }

func (p Preparer) Prepare(ctx context.Context, id string, rule []byte) ([]byte, error) {
	if p.Compiler == nil {
		return nil, domain.ErrUnsupported
	}
	// RawMessage retains the native rule schema. The instance compiler supplies
	// its actual executable, environment, assets and joined converter lifetime.
	data, err := json.Marshal(struct {
		Routing struct {
			Rules []json.RawMessage `json:"rules"`
		} `json:"routing"`
	}{Routing: struct {
		Rules []json.RawMessage `json:"rules"`
	}{[]json.RawMessage{rule}}})
	if err != nil {
		return nil, errors.Join(domain.ErrInvalidArgument, err)
	}
	compiled, err := p.Compiler.Compile(ctx, id, []configdomain.Source{{Name: "routing.json", Content: data}})
	if err != nil {
		return nil, err
	}
	return Extract(ctx, compiled.Content)
}
func Extract(ctx context.Context, content []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(content) > domain.MaxNativeBytes {
		return nil, domain.ErrCapacity
	}
	var config core.Config
	if err := proto.Unmarshal(content, &config); err != nil {
		return nil, errors.Join(domain.ErrInvalidResponse, err)
	}
	var result []byte
	found := false
	for _, app := range config.App {
		if app.GetType() != "xray.app.router.Config" {
			continue
		}
		if found {
			return nil, domain.ErrInvalidResponse
		}
		found = true
		var native router.Config
		if err := proto.Unmarshal(app.Value, &native); err != nil {
			return nil, errors.Join(domain.ErrInvalidResponse, err)
		}
		if len(native.Rule) != 1 || native.Rule[0] == nil || len(native.BalancingRule) != 0 || native.Script != "" {
			return nil, domain.ErrInvalidResponse
		}
		result = bytes.Clone(app.Value)
	}
	if !found {
		return nil, domain.ErrInvalidResponse
	}
	return result, ctx.Err()
}
