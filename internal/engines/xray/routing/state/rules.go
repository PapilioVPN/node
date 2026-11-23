package state

import (
	"bytes"
	"context"
	"errors"
	"time"
)

var ErrRuleExists = errors.New("Xray routing rule tag already exists")

type RuleMode string

const (
	AppendRule   RuleMode = "append"
	ReplaceRules RuleMode = "replace"
)

type RuleChange struct {
	Source         []byte
	Mode           RuleMode
	ConfirmReplace bool
}

func (s *Service) AddRoutingRule(ctx context.Context, id string, change RuleChange) error {
	if change.Mode == "" {
		change.Mode = AppendRule
	}
	source := bytes.TrimSpace(change.Source)
	if id == "" || len(source) == 0 || len(change.Source) > 32<<10 || source[0] != '{' ||
		(change.Mode != AppendRule && change.Mode != ReplaceRules) ||
		change.ConfirmReplace != (change.Mode == ReplaceRules) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.controller.AddRoutingRule(ctx, id, source, change.Mode == AppendRule)
}

func (s *Service) RemoveRoutingRule(ctx context.Context, id, tag string) error {
	if id == "" || tag == "" || !validTag(tag) {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.controller.RemoveRoutingRule(ctx, id, tag)
}
