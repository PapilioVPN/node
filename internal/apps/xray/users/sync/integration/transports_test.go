//go:build integration && linux

package integration_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNativeTransportParityAndChunkPreview(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	for _, kind := range []string{"http", "grpc", "nats"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			o := startTransport(t, f, kind, pki, "tls://"+broker.Addr().String())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			revision := o.revision(t)
			present := true
			r := &v1.SyncRequest{InstanceId: "one", OperationId: "public", Expected: revision, Mode: v1.Mode_DELTA, Selection: []string{"selected"}, Sets: []*v1.UserSet{{InboundTag: "selected", Accounts: [][]byte{nativeUser(0)}, MembershipPresent: &present}}}
			var preview *v1.Preview
			if o.grpc != nil {
				var err error
				preview, err = o.grpc.PreviewSync(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					r.InstanceId = ""
				}
				preview = &v1.Preview{}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/plans", "xray.users.sync.v1.preview", r, preview)
			}
			if preview.Differences[0].Added != 1 {
				t.Fatal(preview)
			}
			apply := &v1.ApplyRequest{InstanceId: "one", PlanId: preview.PlanId, Expected: preview.Revision}
			result := &v1.Result{}
			if o.grpc != nil {
				var err error
				result, err = o.grpc.ApplySync(ctx, apply)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					apply.InstanceId = ""
				}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/applications", "xray.users.sync.v1.apply", apply, result)
			}
			if result.Effect != "completed" || result.Error != "" || len(result.Steps) != 1 || !result.Steps[0].Acknowledged {
				t.Fatal(result)
			}
			if err := traffic(f.address, f.target, 0); err != nil {
				t.Fatal(err)
			}
			// The caller knows the operation ID before dispatch and can recover a
			// response without sending another native write.
			f.calls.take()
			status := &v1.StatusRequest{InstanceId: "one", OperationId: "public"}
			result = &v1.Result{}
			if o.grpc != nil {
				var err error
				result, err = o.grpc.GetSyncStatus(ctx, status)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var input *v1.StatusRequest = status
				if kind == "http" {
					input = nil
				}
				o.json(t, "GET", "/v1/xray/users/instances/one/sync/operations/public", "xray.users.sync.v1.status", input, result)
			}
			if result.Effect != "completed" || len(f.calls.take()) != 0 {
				t.Fatal(result)
			}
			header := &v1.SyncRequest{InstanceId: "one", OperationId: "chunked", Expected: o.revision(t), Mode: v1.Mode_DELTA, Selection: []string{"selected"}}
			begin := &v1.BeginRequest{Request: header, Items: 1, Bytes: uint32(len(nativeUser(1)))}
			transfer := &v1.Transfer{}
			if o.grpc != nil {
				var err error
				transfer, err = o.grpc.BeginSyncTransfer(ctx, begin)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					header.InstanceId = ""
				}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/transfers", "xray.users.sync.transfers.v1.begin", begin, transfer)
			}
			chunk := &v1.ChunkRequest{InstanceId: "one", TransferId: transfer.Id, Additions: []*v1.Addition{{InboundTag: "selected", Configuration: nativeUser(1)}}, Final: true}
			if o.grpc != nil {
				if _, err := o.grpc.AppendSyncChunk(ctx, chunk); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "http" {
					chunk.InstanceId = ""
					chunk.TransferId = ""
				}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/transfers/"+transfer.Id+"/chunks", "xray.users.sync.transfers.v1.append", chunk, &v1.Transfer{})
			}
			finish := &v1.TransferRequest{InstanceId: "one", TransferId: transfer.Id}
			if o.grpc != nil {
				p, err := o.grpc.PreviewSyncTransfer(ctx, finish)
				if err != nil || p.Differences[0].Added != 1 {
					t.Fatal(p, err)
				}
			} else {
				var input *v1.TransferRequest = finish
				if kind == "http" {
					input = nil
				}
				p := &v1.Preview{}
				o.json(t, "POST", "/v1/xray/users/instances/one/sync/transfers/"+transfer.Id+"/previews", "xray.users.sync.transfers.v1.preview", input, p)
				if p.Differences[0].Added != 1 {
					t.Fatal(p)
				}
			}
			if err := traffic(f.address, f.target, 1); err == nil {
				t.Fatal("finalizing transfer applied accounts without approval")
			}
		})
	}
}
