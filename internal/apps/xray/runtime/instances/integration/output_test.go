//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeOutputReattachedThroughNormalLogsBinding(t *testing.T) {
	pki := testutil.NewPKI(t)
	e := recoverableEngine(t, "console")
	config := nodeConfig(t, pki, "", "http", e)
	first, c, sources := startObservedNode(t, pki, config)
	awaitReady(t, c, "http", e.settings.ID)
	original := childRecord(t, e)
	before, err := sources[0].Source.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: original.ExecutionID})
	if err != nil || len(before.Data) == 0 {
		t.Fatal("native prefix", err)
	}
	requireExtended(t, c, "http", "retention", e.settings.ID, map[string]any{"execution_id": original.ExecutionID, "preserve": true})
	if err := first.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, c, sources = startObservedNode(t, pki, config)
	awaitReady(t, c, "http", e.settings.ID)
	after, err := sources[0].Source.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: original.ExecutionID})
	if err != nil || !after.Reattached || after.ExecutionID != before.ExecutionID || after.Generation != before.Generation || !bytes.Equal(before.Data, after.Data) {
		t.Fatal("reattached console lost/corrupted prefix", err)
	}
	if _, err := c.control(context.Background(), "http", "stop", e.settings.ID); err != nil {
		t.Fatal(err)
	}
	exited, err := sources[0].Source.ReadOutput(context.Background(), domain.OutputRequest{ExecutionID: original.ExecutionID})
	if err != nil || !exited.Exited || !exited.PositionUncertain || exited.NextOffset > domain.MaxOutputRead {
		t.Fatal("adopted exit hid gap", exited, err)
	}
}
