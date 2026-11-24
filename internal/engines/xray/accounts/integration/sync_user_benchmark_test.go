package integration_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/protobuf/proto"
)

func BenchmarkSyncUserGRPC(b *testing.B) {
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprintf("existing=%d", count), func(b *testing.B) {
			users := make([]configuredAccount, count)
			for i := range users {
				users[i] = configuredAccount{
					Email: fmt.Sprintf("profile-%05d@example.invalid", i),
					ID:    fmt.Sprintf("7ccb2c70-6a53-4a6a-85e5-%012x", i+1),
				}
			}
			extra := configuredAccount{Email: "extra@example.invalid", ID: rotatedCredential}
			for _, workload := range []struct {
				name           string
				target         configuredAccount
				added, removed uint32
			}{
				{name: "add", target: extra, added: 1},
				{name: "remove", target: users[0], removed: 1},
				{name: "no-change", target: users[0]},
			} {
				b.Run(workload.name, func(b *testing.B) {
					f := startFixture(b, users...)
					f.acceptConfiguredUsers(b, users)
					f.verifyProfileMembers(b, users)
					present := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: workload.target.Email, Uuid: &workload.target.ID}
					absent := &accountsv1.SyncUserRequest{Id: instanceID, InboundTag: "vless", Email: workload.target.Email}
					request, restore := present, absent
					if workload.removed == 1 {
						request, restore = absent, present
					}
					call := func(req *accountsv1.SyncUserRequest, added, removed uint32) {
						b.Helper()
						result, err := f.grpc.SyncUser(b.Context(), req)
						require(b, err)
						expectSync(b, result, added, removed)
					}
					call(request, workload.added, workload.removed)
					switch workload.name {
					case "add":
						f.verifyProfileMembers(b, append(users, extra))
					case "remove":
						f.verifyProfileMembers(b, users[1:])
					default:
						f.verifyProfileMembers(b, users)
					}
					if workload.added+workload.removed != 0 {
						call(restore, workload.removed, workload.added)
					}
					var latencies []time.Duration
					b.ReportAllocs()
					for b.Loop() {
						start := time.Now()
						call(request, workload.added, workload.removed)
						latencies = append(latencies, time.Since(start))
						if workload.added+workload.removed != 0 {
							b.StopTimer()
							call(restore, workload.removed, workload.added)
							b.StartTimer()
						}
					}
					slices.Sort(latencies)
					for _, percentile := range []int{50, 95} {
						index := (len(latencies)*percentile+99)/100 - 1
						b.ReportMetric(float64(latencies[index].Nanoseconds()), fmt.Sprintf("p%d-ns", percentile))
					}
					f.verifyProfileMembers(b, users)
				})
			}
		})
	}
}

// Mark the configured accounts as accepted for replay without native writes.
func (f fixture) acceptConfiguredUsers(b *testing.B, users []configuredAccount) {
	b.Helper()
	for start := 0; start < len(users); start += membership.MaxSyncAccounts {
		ctx, cancel := context.WithTimeout(b.Context(), 10*time.Second)
		stream, err := f.grpc.SyncUsersChunked(ctx)
		require(b, err)
		end := min(start+membership.MaxSyncAccounts, len(users))
		for offset := start; offset < end; offset += membership.MaxChunkAccounts {
			last := min(offset+membership.MaxChunkAccounts, end)
			chunk := &accountsv1.UsersChunk{Id: instanceID, InboundTag: "vless",
				Index: uint32((offset - start) / membership.MaxChunkAccounts), Last: last == end}
			for i := offset; i < last; i++ {
				chunk.Accounts = append(chunk.Accounts, &accountsv1.VLESSAccount{Email: users[i].Email, Uuid: &users[i].ID})
			}
			require(b, stream.Send(chunk))
		}
		result, err := stream.CloseAndRecv()
		cancel()
		require(b, err)
		expectSync(b, result, 0, 0)
	}
}

func (f fixture) verifyProfileMembers(b *testing.B, want []configuredAccount) {
	b.Helper()
	expected := make(map[string]string, len(want))
	for _, user := range want {
		expected[user.Email] = user.ID
	}
	got := f.nativeUsers(b, "vless")
	if len(got) != len(want) {
		b.Fatalf("native VLESS user count = %d, want %d", len(got), len(want))
	}
	for _, user := range got {
		var account native.VlessAccount
		require(b, proto.Unmarshal(user.GetAccount().GetValue(), &account))
		if id, ok := expected[user.GetEmail()]; !ok || id != account.Id {
			b.Fatalf("unexpected membership for %q", user.GetEmail())
		}
		delete(expected, user.GetEmail())
	}
}
