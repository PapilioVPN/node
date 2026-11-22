//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealOutboundPagesUseOneListForLargeInventory(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, count := range []int{1, 50, 51, 1000} {
			t.Run(fmt.Sprintf("%s/%d", transport, count), func(t *testing.T) {
				f := newFixture(t, pki, broker, transport)
				path := f.config.Instances[0].Xray.Configuration
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]any
				json.Unmarshal(data, &config)
				outbounds := make([]any, count)
				for i := range outbounds {
					outbounds[i] = map[string]any{"tag": fmt.Sprintf("out-%05d", i), "protocol": "blackhole"}
				}
				config["outbounds"] = outbounds
				data, _ = json.Marshal(config)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				relay := newRelay(t, &f)
				c := startNode(t, f)
				awaitNative(t, f.native)
				token := ""
				originalToken := ""
				total := 0
				seen := map[string]bool{}
				var observed time.Time
				for {
					result := page(t, c, transport, token)
					if result.Error != "" || len(result.Outbounds) > 50 || result.Generation != 1 || result.DefaultState != "unobservable" {
						t.Fatal(result)
					}
					if observed.IsZero() {
						observed = result.ObservedAt
					}
					if observed != result.ObservedAt {
						t.Fatal("continuation refetched")
					}
					for _, r := range result.Outbounds {
						if seen[r.Tag] {
							t.Fatal("duplicate page row", r.Tag)
						}
						seen[r.Tag] = true
					}
					if originalToken == "" {
						originalToken = result.NextPageToken
					}
					total += len(result.Outbounds)
					token = result.NextPageToken
					if token == "" {
						break
					}
				}
				if total != count || relay.lists.Load() != 1 || relay.adds.Load() != 0 || relay.removes.Load() != 0 {
					t.Fatal(total, relay.lists.Load())
				}
				if count == 51 {
					restartExecution(t, c, transport)
					awaitNative(t, f.native)
					stale := page(t, c, transport, originalToken)
					if stale.Error != "continuation_expired" || relay.lists.Load() != 1 {
						t.Fatal(stale, relay.lists.Load())
					}
				}

			})
		}
	}
}
