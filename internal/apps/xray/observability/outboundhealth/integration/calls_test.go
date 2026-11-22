//go:build integration && linux

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/outboundhealth/infra/api"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealCollectionTraversalHasOneNativeRPC(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	f := newFixture(t, pki, strings.Replace(broker.ClientURL(), "nats://", "tls://", 1), "grpc", "ordinary", 49)
	deadline := time.Now().Add(4 * time.Second)
	for {
		if p := f.list(t, "", 50); p.Total == 51 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native probes incomplete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	conn := &countConnection{ClientConnInterface: f.native}
	service, err := services.New([]services.Binding{{ID: "edge", Reader: api.New(conn)}}, fixedStatus{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	first, err := service.List(ctx, "edge", domain.PageRequest{PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.List(ctx, "edge", domain.PageRequest{PageToken: first.NextPageToken})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Statuses) != 50 || len(second.Statuses) != 1 || conn.calls != 1 || !first.ObservedAt.Equal(second.ObservedAt) {
		t.Fatal(len(first.Statuses), len(second.Statuses), conn.calls, err)
	}
	// An exact selection also needs one full native RPC because Xray has no filter.
	if _, err := service.Get(ctx, "edge", domain.Selection{Tag: "probe-good"}); err != nil || conn.calls != 2 {
		t.Fatal(err, conn.calls)
	}
	t.Log("real Xray: 51 records, pages 50+1, one collection RPC; exact selection adds one RPC")
}
