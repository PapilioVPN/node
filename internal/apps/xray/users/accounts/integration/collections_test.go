//go:build integration && linux

package integration_test

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

func TestLiveAccountPagesReadNativeCollectionOnceAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		for _, count := range []int{0, 1, 50, 51, 1000} {
			t.Run(fmt.Sprintf("%s/%d", transport, count), func(t *testing.T) {
				f := newFixture(t, pki, broker, "all")
				collectionSource(t, f, count, 0)
				relayAddress, relay := accountRelay(t, f.native)
				f.config.Instances[0].Xray.API.Address = relayAddress
				f.config.Communications.GRPC.MaxSendMessageSize = 2 << 20
				c := startNode(t, f)
				awaitNative(t, f.native)
				for _, view := range []string{"metadata", "export"} {
					before := relay.users.Load()
					p := accountView(t, c, transport, view, "members", "")
					if p.Error != "" || len(p.Users) > 50 || p.Cost.Limit != domain.MaxCollectionBytes || int64(p.Cost.Bytes) != relay.bytes.Load() {
						t.Fatal(p)
					}
					if count != 0 && p.Cost.Bytes == 0 {
						t.Fatal("native read cost missing")
					}
					total := len(p.Users)
					cursor := p.Next
					for cursor != "" {
						next := accountView(t, c, transport, view, "members", cursor)
						if next.Error != "" || len(next.Users) > 50 || !next.Observation.Observed.Equal(p.Observation.Observed) || next.Cost.Bytes != p.Cost.Bytes {
							t.Fatal(next)
						}
						total += len(next.Users)
						cursor = next.Next
					}
					if total != count || relay.users.Load()-before != 1 {
						t.Fatal("full read repeated", total, count, relay.users.Load()-before)
					}
					data := accountViewData(t, c, transport, view, "members", "")
					if view == "metadata" && bytes.Contains(data, []byte("configuration")) {
						t.Fatal("metadata exposed native User bytes")
					}
					if view == "export" {
						for _, user := range p.Users {
							var native protocol.User
							if proto.Unmarshal(user.Configuration, &native) != nil || native.Account == nil || native.Email != user.Email {
								t.Fatal("export lost native account")
							}
						}
					}
					t.Logf("view=%s users=%d complete_native_payload=%d native_limit=%d engine_rpc_ns=%d node_native_rpc_ns=%d page_bytes=%d", view, count, p.Cost.Bytes, p.Cost.Limit, relay.nanoseconds.Load(), p.Cost.Duration, len(data))
				}
				if count > 50 {
					p := accountView(t, c, transport, "metadata", "members", "")

					calls := relay.users.Load()
					applied, code := accountMutation(t, c, transport, "add", "", accountConfiguration(t, "managed@example.test"))
					if code != "" || !applied.Acknowledged {
						t.Fatal(applied, code)
					}
					observedTotal := len(p.Users)
					cursor := p.Next
					for cursor != "" {
						next := accountView(t, c, transport, "metadata", "members", cursor)
						if next.Error != "" {
							t.Fatal(next)
						}
						observedTotal += len(next.Users)
						cursor = next.Next
					}
					if observedTotal != count || relay.users.Load() != calls {
						t.Fatal("local mutation refreshed retained observation", observedTotal, relay.users.Load()-calls)
					}
					restartAccountInstance(t, c)
					before := relay.users.Load()
					stale := accountView(t, c, transport, "metadata", "members", p.Next)
					if stale.Error != "stale_observation" || relay.users.Load() != before {
						t.Fatal("restart continuation fetched again", stale, relay.users.Load()-before)
					}
				}
			})
		}
	}
}
func TestNativeAccountReplyLimitIsExplicitAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			collectionSource(t, f, 1500, 3000)
			relayAddress, relay := accountRelay(t, f.native)
			f.config.Instances[0].Xray.API.Address = relayAddress
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			for _, view := range []string{"metadata", "export"} {
				p := accountView(t, c, transport, view, "members", "")
				if p.Error != "resource_exhausted" || len(p.Users) != 0 || relay.bytes.Load() <= domain.MaxCollectionBytes {
					t.Fatal("native overflow became partial success", p, relay.bytes.Load())
				}
			}
			t.Logf("complete_native_payload=%d exceeds_client_limit=%d", relay.bytes.Load(), domain.MaxCollectionBytes)
		})
	}
}
