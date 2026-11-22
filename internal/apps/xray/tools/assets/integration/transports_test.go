//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeAssetModuleTransportParity(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := assetBroker(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := setup(t)
			c := management(t, f, pki, broker, transport)
			old := c.inspect(t)
			var next domain.Release
			data := c.call(t, "stage", map[string]any{"candidate": candidate(t, "PROOF", transport+".test")})
			if err := json.Unmarshal(data, &next); err != nil || next.ID == "" {
				t.Fatal("stage", string(data), err)
			}
			if c.inspect(t).State != old.State {
				t.Fatal("transport staging selected release")
			}
			invoke := func(op, target string) domain.Receipt {
				inv := c.inspect(t)
				q := map[string]any{"revision": inv.State.Revision, "expected_revision": strconv.FormatUint(inv.State.Revision, 10), "execution_id": f.runtime.Observe().ExecutionID}
				if target != "" {
					q["release_id"] = target
				}
				data := c.call(t, op, q)
				var result struct {
					domain.Receipt
					ErrorCode string `json:"error_code"`
				}
				if err := json.Unmarshal(data, &result); err != nil || result.ErrorCode != "" || !result.Ready {
					t.Fatal("activation receipt", string(data), err)
				}
				return result.Receipt
			}
			result := invoke("activate", next.ID)
			if result.State.Active != next.ID || result.State.Previous != old.State.Active {
				t.Fatal(result)
			}
			f.traffic(t)
			result = invoke("rollback", "")
			if result.State.Active != old.State.Active || result.State.Previous != next.ID {
				t.Fatal(result)
			}
			f.traffic(t)
			// An explicit recovery without intent is a no-effect conflict in every wire.
			inv := c.inspect(t)
			q := map[string]any{"revision": inv.State.Revision, "expected_revision": strconv.FormatUint(inv.State.Revision, 10), "execution_id": f.runtime.Observe().ExecutionID}
			data = c.call(t, "recover", q)
			var rejected struct {
				ErrorCode string `json:"error_code"`
			}
			if json.Unmarshal(data, &rejected) != nil || rejected.ErrorCode != "conflict" {
				t.Fatal("recovery precondition", string(data))
			}
			f.traffic(t)
		})
	}
}
