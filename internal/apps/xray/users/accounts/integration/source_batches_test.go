//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestLiveSourceAccountBatchesAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := accountTarget(t)
			f := newFixture(t, pki, broker, transport)
			address := allowAccountTraffic(t, f, target.Listener.Addr().String())
			f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
			f.config.Communications.Requests.Timeout = 10 * time.Second
			if h := f.config.Communications.HTTP; h != nil {
				h.BodyLimit, h.WriteTimeout = 2<<20, 12*time.Second
			}
			if g := f.config.Communications.GRPC; g != nil {
				g.MaxRecvMessageSize, g.MaxSendMessageSize = 2<<20, 2<<20
			}
			if n := f.config.Communications.Requests.NATS; n != nil {
				n.PendingBytes = 2 << 20
			}
			original, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			for _, size := range []int{0, 1, 50, 51, 1000} {
				t.Run(fmt.Sprint(size), func(t *testing.T) {
					selections := make([]domain.SourceSelection, size)
					removals := make([]domain.Removal, size)
					members := make([]map[string]any, size)
					for i := range members {
						email := fmt.Sprintf("source-%d@example.test", i)
						members[i] = map[string]any{"email": email, "id": fmt.Sprintf("00000000-0000-4000-8000-%012x", i+100)}
						// Selection order differs from native source order.
						selections[size-1-i] = domain.SourceSelection{InboundTag: "members", Email: email}
						removals[size-1-i] = domain.Removal{InboundTag: "members", Email: email}
					}
					content, _ := json.Marshal(map[string]any{"inbounds": []any{map[string]any{"tag": "members", "port": 1, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": members}}}})
					files := []configs.Source{{Name: "base.json", Content: []byte(`{"outbounds":[{"protocol":"blackhole"}]}`)}, {Name: "tail.json", Content: content}}
					result := sourceBatchAccounts(t, c, transport, selections, files, 4)
					if size == 0 {
						if result.Error != "invalid_argument" || len(result.Items) != 0 {
							t.Fatal(result)
						}
						return
					}
					if result.Error != "" || result.Operation.Effect != "completed" || len(result.Items) != size {
						t.Fatal(result)
					}
					for i, item := range result.Items {
						if item.Index != i || item.Error != "" || !item.Attempted || !item.Operation.Acknowledged || item.Operation.Email != selections[i].Email {
							t.Fatal(i, item)
						}
					}
					assertFamilyCount(t, c, transport, int64(size+2))
					id := fmt.Sprintf("00000000-0000-4000-8000-%012x", size+99)
					connection, _, err := accountTunnel(address, id, target.Listener.Addr().String())
					if err != nil {
						t.Fatal("source batch did not authenticate", err)
					}
					connection.Close()
					removed := batchAccounts(t, c, transport, nil, removals, 4)
					if removed.Error != "" {
						t.Fatal(removed)
					}
					assertFamilyCount(t, c, transport, 2)
					if connection, _, err := accountTunnel(address, id, target.Listener.Addr().String()); err == nil {
						connection.Close()
						t.Fatal("removed batch account still authenticates")
					}
					// A missing source account is a local per-item failure between
					// one confirmed effect and an unattempted ordered tail.
					if size == 51 {
						selections[1].Email = "absent"
						partial := sourceBatchAccounts(t, c, transport, selections, files, 1)
						if partial.Error != "invalid_argument" || partial.Operation.Effect != "partial" || !partial.Items[0].Operation.Acknowledged || partial.Items[1].Attempted || partial.Items[1].Error != "invalid_argument" || partial.Items[2].Attempted || partial.Items[2].Error != "not_attempted" {
							t.Fatal(partial)
						}
						if removed := batchAccounts(t, c, transport, nil, removals[:1], 1); removed.Error != "" {
							t.Fatal(removed)
						}
					}
				})
			}
			current, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
			if err != nil || string(current) != string(original) {
				t.Fatal("live source batch changed original configuration", err)
			}
		})
	}
}

func TestNativeFamilySourceBatchAuthenticationAndRevocation(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, family := range nativeAccountFamilies() {
		t.Run(family.name, func(t *testing.T) {
			for _, transport := range []string{"http", "grpc", "nats"} {
				t.Run(transport, func(t *testing.T) {
					const email = "Batch@example.test"
					target := accountTarget(t)
					f := newFixture(t, pki, broker, transport)
					configureFamily(t, &f, family)
					address, virtualTarget := familyTrafficTarget(t, f, target.Listener.Addr().String())
					before, beforeAPI := familyTrafficClient(t, &f, family, "before", address, email, 3)
					after, afterAPI := familyTrafficClient(t, &f, family, "after", address, email, 3)
					f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
					f.config.Communications.Requests.Timeout = 2 * time.Second
					if h := f.config.Communications.HTTP; h != nil {
						h.WriteTimeout = 3 * time.Second
					}
					c := startNode(t, f)
					for _, api := range []string{f.native, beforeAPI, afterAPI} {
						awaitNative(t, api)
					}
					result := sourceBatchAccounts(t, c, transport, []domain.SourceSelection{{InboundTag: "members", Email: email}}, []configs.Source{{Name: "account.json", Content: familySource(t, family, email, 3)}}, 1)
					if result.Error != "" || !result.Operation.Acknowledged {
						t.Fatal(result)
					}
					if err := familyTraffic(t, before, virtualTarget); err != nil {
						t.Fatal("batch source account failed native authentication", err)
					}
					removed := batchAccounts(t, c, transport, nil, []domain.Removal{{InboundTag: "members", Email: email}}, 1)
					if removed.Error != "" || !removed.Operation.Acknowledged {
						t.Fatal(removed)
					}
					if familyTraffic(t, after, virtualTarget) == nil {
						t.Fatal("removed account authenticated from a fresh client")
					}
					assertFamilyCount(t, c, transport, 1)
				})
			}
		})
	}
}
