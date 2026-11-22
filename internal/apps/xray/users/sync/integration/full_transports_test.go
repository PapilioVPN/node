//go:build integration && linux

package integration_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeFullTransportParity(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, kind := range []string{"http", "grpc", "nats"} {
		t.Run(kind, func(t *testing.T) {
			f := withFull(t, newFixture(t))
			o := startTransport(t, f.fixture, kind, pki, "tls://"+broker.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			present := true
			r := &v1.SyncRequest{InstanceId: "one", OperationId: "full-public", Expected: o.revision(t), Mode: v1.Mode_FULL, Selection: []string{"selected"}, Sets: []*v1.UserSet{{InboundTag: "selected", Accounts: [][]byte{nativeUser(0)}, MembershipPresent: &present}}}
			p := &v1.Preview{}
			if o.grpc != nil {
				preview, err := o.grpc.PreviewSync(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				p = preview
			} else {
				if kind == "http" {
					r.InstanceId = ""
				}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/plans", "xray.users.sync.v1.preview", r, p)
			}
			if p.ConfigOperationId == "" || p.ApplyMode != "restart" || f.compiler.calls != 1 {
				t.Fatal(p, f.compiler.calls)
			}
			apply := &v1.ApplyRequest{InstanceId: "one", PlanId: p.PlanId, Expected: p.Revision}
			result := &v1.Result{}
			if o.grpc != nil {
				response, err := o.grpc.ApplySync(ctx, apply)
				if err != nil {
					t.Fatal(err)
				}
				result = response
			} else {
				if kind == "http" {
					apply.InstanceId = ""
				}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/applications", "xray.users.sync.v1.apply", apply, result)
			}
			if result.Effect != "completed" || !result.Durable || !result.Ready || result.Error != "" {
				t.Fatal(result)
			}
			if err := traffic(f.address, f.target, 0); err != nil {
				t.Fatal(err)
			}
			if err := traffic(f.excluded, f.target, 4095); err != nil {
				t.Fatal("excluded inbound", err)
			}
			// This identity can also be read after every process-owned sync cache
			// disappears, since it is the Configs durable application identity.
			status := &v1.StatusRequest{InstanceId: "one", OperationId: p.ConfigOperationId}
			recovered := &v1.Result{}
			if o.grpc != nil {
				response, err := o.grpc.GetSyncStatus(ctx, status)
				if err != nil {
					t.Fatal(err)
				}
				recovered = response
			} else {
				var input *v1.StatusRequest = status
				if kind == "http" {
					input = nil
				}
				o.json(t, "GET", "/v1/xray/users/instances/one/sync/operations/"+p.ConfigOperationId, "xray.users.sync.v1.status", input, recovered)
			}
			if !recovered.Durable || !recovered.Ready || recovered.ConfigOperationId != p.ConfigOperationId {
				t.Fatal(recovered)
			}
		})
	}
}
