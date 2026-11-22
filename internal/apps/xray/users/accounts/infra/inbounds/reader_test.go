package inbounds_test

import (
	"context"
	"errors"
	"testing"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/inbounds"
)

type catalog struct{ calls int }

func (*catalog) Compatibility(context.Context, string, string) (upstream.Compatibility, error) {
	panic("per-tag lookup in bulk selection")
}
func (c *catalog) List(_ context.Context, _ string, r upstream.PageRequest) (upstream.Summaries, error) {
	c.calls++
	if r.Cursor == "" {
		return upstream.Summaries{Generation: 1, Inbounds: []upstream.Summary{{Tag: "duplicate", AccountType: "first"}, {Tag: "unique", AccountType: "account", LiveAccounts: true}}, PageEvidence: upstream.PageEvidence{NextCursor: "continued"}}, nil
	}
	return upstream.Summaries{Generation: 1, Inbounds: []upstream.Summary{{Tag: "duplicate", AccountType: "second"}}}, nil
}
func TestBulkSelectionScansOnceAndDetectsAmbiguityBeyondFirstPage(t *testing.T) {
	source := &catalog{}
	reader := inbounds.Reader{Source: source}
	result, err := reader.Select(context.Background(), "a", []string{"unique", "duplicate", "missing"})
	if err != nil || source.calls != 2 || result[0].AccountType != "account" || !errors.Is(result[1].Err, domain.ErrInboundAmbiguous) || !errors.Is(result[2].Err, domain.ErrInboundMissing) {
		t.Fatal(result, err, source.calls)
	}
}
