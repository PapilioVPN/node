//go:build integration && linux

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// This measures the actual pinned engine's full RPC, Node SDK decode/projection
// and Go client allocations. It excludes controller transports and startup, and
// does not measure engine CPU/RSS or establish a comparator performance claim.
func BenchmarkNativeAccountCollection(b *testing.B) {
	pki := testutil.NewPKI(b)
	for _, count := range []int{0, 1, 50, 51, 1000} {
		for _, view := range []string{"metadata", "export"} {
			b.Run(fmt.Sprintf("%s/%d", view, count), func(b *testing.B) {
				f := newFixture(b, pki, "", "grpc")
				collectionSource(b, f, count, 0)
				startNode(b, f)
				awaitNative(b, f.native)
				connection, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
				if err != nil {
					b.Fatal(err)
				}
				b.Cleanup(func() { connection.Close() })
				reader := api.New(connection)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				warm, err := reader.Metadata(ctx, "members")
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if view == "metadata" {
						_, err = reader.Metadata(ctx, "members")
					} else {
						_, err = reader.Records(ctx, "members")
					}
					if err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(warm.NativeBytes), "native-B/op")
			})
		}
	}
}
