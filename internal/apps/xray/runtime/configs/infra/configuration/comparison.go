// Package configuration inspects native compiled projections and source context.
package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"path"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/app/commander"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	jsonreader "github.com/xtls/xray-core/infra/conf/json"
	"google.golang.org/protobuf/proto"
)

type Inspector struct{}

// Environment deliberately supports JSON/JSONC and native protobuf only. Other
// changed source formats take the original-source restart path, preserving Env.
// This decoder never Build()s a native config or changes process environment.
func Environment(sources []domain.Source) (map[string]string, bool) {
	result := map[string]string{}
	for _, source := range sources {
		switch strings.ToLower(path.Ext(source.Name)) {
		case ".pb", ".protobuf":
		case ".json", ".jsonc":
			var projection struct {
				Env map[string]string `json:"env"`
			}
			if json.NewDecoder(&jsonreader.Reader{Reader: bytes.NewReader(source.Content)}).Decode(&projection) != nil {
				return nil, false
			}
			maps.Copy(result, projection.Env)
		default:
			return nil, false
		}
	}
	return result, true
}

func split(config *core.Config) (*core.Config, *router.Config, error) {
	root := &core.Config{Inbound: config.Inbound, Extension: config.Extension}
	// The pinned handler construction never consumes expire/comment. Preserve
	// them in original sources without restarting for an ineffective edit.
	for _, outbound := range config.Outbound {
		if outbound == nil {
			root.Outbound = append(root.Outbound, nil)
			continue
		}
		projection := &core.OutboundHandlerConfig{Tag: outbound.Tag, SenderSettings: outbound.SenderSettings, ProxySettings: outbound.ProxySettings}
		projection.ProtoReflect().SetUnknown(outbound.ProtoReflect().GetUnknown())
		root.Outbound = append(root.Outbound, projection)
	}
	root.ProtoReflect().SetUnknown(config.ProtoReflect().GetUnknown())
	var routing *router.Config
	for _, app := range config.App {
		if app != nil && app.Type == "xray.app.router.Config" {
			if routing != nil {
				return nil, nil, domain.ErrInvalidArgument
			}
			routing = &router.Config{}
			if proto.Unmarshal(app.Value, routing) != nil {
				return nil, nil, domain.ErrInvalidArgument
			}
			marker := &serial.TypedMessage{Type: app.Type}
			marker.ProtoReflect().SetUnknown(app.ProtoReflect().GetUnknown())
			root.App = append(root.App, marker)
		} else {
			root.App = append(root.App, app)
		}
	}
	return root, routing, nil
}
func routingBase(c *router.Config) *router.Config {
	result := &router.Config{DomainStrategy: c.DomainStrategy, Script: c.Script, BalancingRule: c.BalancingRule}
	result.ProtoReflect().SetUnknown(c.ProtoReflect().GetUnknown())
	return result
}

func routingDeclared(c *core.Config) bool {
	for _, app := range c.App {
		if app == nil || app.Type != "xray.app.commander.Config" {
			continue
		}
		var config commander.Config
		if proto.Unmarshal(app.Value, &config) != nil {
			return false
		}
		for _, service := range config.Service {
			if service != nil && service.Type == "xray.app.router.command.Config" {
				return true
			}
		}
	}
	return false
}
func decode(content []byte) (*core.Config, error) {
	if len(content) > domain.MaxCompiledBytes {
		return nil, domain.ErrTooLarge
	}
	result := &core.Config{}
	if proto.Unmarshal(content, result) != nil {
		return nil, domain.ErrInvalidArgument
	}
	return result, nil
}

// Compare makes no engine calls. Rule-only live replacement reuses the existing
// native single-rule adapter. Other outbound/default/handler/feature changes
// retain their original sources and require execution replacement.
func (Inspector) Compare(ctx context.Context, oldSources, newSources []domain.Source, before, after []byte) (domain.Assessment, error) {
	if err := ctx.Err(); err != nil {
		return domain.Assessment{}, err
	}
	a, err := decode(before)
	if err != nil {
		return domain.Assessment{}, err
	}
	b, err := decode(after)
	if err != nil {
		return domain.Assessment{}, err
	}
	ar, ra, err := split(a)
	if err != nil {
		return domain.Assessment{}, err
	}
	br, rb, err := split(b)
	if err != nil {
		return domain.Assessment{}, err
	}
	assessment := domain.Assessment{Mode: domain.Restart, Reason: "native_sections_require_restart", Differences: []domain.Difference{}}
	if !proto.Equal(&core.Config{Inbound: a.Inbound}, &core.Config{Inbound: b.Inbound}) {
		assessment.Differences = append(assessment.Differences, domain.Difference{Section: "inbounds", Before: uint32(len(a.Inbound)), After: uint32(len(b.Inbound))})
	}
	if !proto.Equal(&core.Config{Outbound: ar.Outbound}, &core.Config{Outbound: br.Outbound}) {
		assessment.Differences = append(assessment.Differences, domain.Difference{Section: "outbounds", Before: uint32(len(a.Outbound)), After: uint32(len(b.Outbound))})
		assessment.Reason = "outbound_default_and_resource_semantics_require_restart"
	}
	if !proto.Equal(&core.Config{App: ar.App, Extension: ar.Extension}, &core.Config{App: br.App, Extension: br.Extension}) || !bytes.Equal(a.ProtoReflect().GetUnknown(), b.ProtoReflect().GetUnknown()) {
		assessment.Differences = append(assessment.Differences, domain.Difference{Section: "features", Before: uint32(len(ar.App)), After: uint32(len(br.App))})
	}
	if !proto.Equal(ra, rb) {
		assessment.Differences = append(assessment.Differences, domain.Difference{Section: "routing", Before: uint32(len(ra.GetRule())), After: uint32(len(rb.GetRule()))})
	}
	oldEnv, oldKnown := Environment(oldSources)
	newEnv, newKnown := Environment(newSources)
	sameEnv := policies.SameSources(oldSources, newSources) || (oldKnown && newKnown && maps.Equal(oldEnv, newEnv))
	if !sameEnv {
		assessment.Differences = append(assessment.Differences, domain.Difference{Section: "source_environment"})
		assessment.Reason = "source_environment_not_proven_equivalent"
		return assessment, nil
	}
	if proto.Equal(ar, br) && proto.Equal(ra, rb) {
		assessment.Mode = domain.Unchanged
		assessment.Reason = "equivalent_native_config_and_source_environment"
		return assessment, nil
	}
	if !proto.Equal(ar, br) || ra == nil || rb == nil {
		return assessment, nil
	}
	if !routingDeclared(a) {
		assessment.Reason = "native_routing_service_not_declared"
		return assessment, nil
	}
	if len(ra.BalancingRule) != 0 || len(rb.BalancingRule) != 0 || len(rb.Rule) != 1 || !proto.Equal(routingBase(ra), routingBase(rb)) {
		assessment.Reason = "routing_shape_or_initialization_requires_restart"
		return assessment, nil
	}
	for _, rule := range append(append([]*router.RoutingRule{}, ra.Rule...), rb.Rule...) {
		if rule == nil || rule.Webhook != nil {
			assessment.Reason = "routing_webhook_lifetime_requires_restart"
			return assessment, nil
		}
	}
	data, err := proto.Marshal(rb)
	if err != nil {
		return domain.Assessment{}, err
	}
	assessment.Mode = domain.LiveRouting
	assessment.Reason = "only_single_rule_routing_changed"
	assessment.Payload = data
	return assessment, ctx.Err()
}

// Embeddable is intentionally stricter than ordinary source replacement: only
// proven empty Env permits replacing original sources with native protobuf.
func Embeddable(sources []domain.Source) bool {
	env, known := Environment(sources)
	return known && len(env) == 0
}
