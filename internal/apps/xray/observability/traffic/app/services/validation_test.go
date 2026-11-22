package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

func TestInvalidInputCannotReachReadDiscoveryOrReset(t *testing.T) {
	for name, selection := range map[string]domain.Selection{
		"scope":     {Scope: "invalid", Name: "a", Direction: domain.Uplink},
		"direction": {Scope: domain.User, Name: "a", Direction: "invalid"},
		"empty":     {Scope: domain.User, Direction: domain.Uplink},
		"oversized": {Scope: domain.User, Name: strings.Repeat("x", 4097), Direction: domain.Uplink},
		"encoding":  {Scope: domain.User, Name: "\xff", Direction: domain.Uplink},
		"nul":       {Scope: domain.User, Name: "a\x00b", Direction: domain.Uplink},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			s, _ := New([]Binding{{ID: "a", Reader: readerFunc(func(context.Context, domain.Selection) (int64, error) { calls++; return 0, nil }), Lister: listFunc(func(context.Context, domain.Scope) ([]domain.Counter, error) { calls++; return nil, nil }), Resetter: resetFunc(func(context.Context, domain.Selection) (int64, error) { calls++; return 0, nil })}}, statusFunc(running))
			if _, err := s.Read(context.Background(), "a", selection); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatal(err)
			}
			r := intent(s, domain.ResetExact)
			r.Selection = selection
			if _, err := s.Reset(context.Background(), "a", r); !errors.Is(err, domain.ErrInvalidArgument) {
				t.Fatal(err)
			}
			if name == "scope" {
				if _, err := s.List(context.Background(), "a", domain.PageRequest{Scope: selection.Scope}); !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatal(err)
				}
				r = intent(s, domain.ResetScope)
				r.Selection.Scope = selection.Scope
				if _, err := s.Reset(context.Background(), "a", r); !errors.Is(err, domain.ErrInvalidArgument) {
					t.Fatal(err)
				}
			}
			if calls != 0 {
				t.Fatal("invalid input reached native work", calls)
			}
		})
	}
}
