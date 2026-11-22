// Package wire owns shared typed sync envelope conversion and safe errors.
package wire

import (
	"context"
	"time"

	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func Decode(data []byte, message proto.Message) error {
	if len(data) > 48<<20 {
		return domain.ErrCapacity
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, message); err != nil {
		return domain.ErrInvalid
	}
	return nil
}
func Encode(message proto.Message) ([]byte, error) {
	return (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
}
func Revision(r domain.Revision) *v1.Revision {
	return &v1.Revision{Epoch: r.Epoch, Control: r.Control, Config: r.Config, Generation: r.Generation}
}
func ReadRevision(r *v1.Revision) (domain.Revision, error) {
	if r == nil || r.Epoch == "" {
		return domain.Revision{}, domain.ErrInvalid
	}
	return domain.Revision{Epoch: r.Epoch, Control: r.Control, Config: r.Config, Generation: r.Generation}, nil
}
func Request(ctx context.Context, r *v1.SyncRequest) (domain.Request, error) {
	if r == nil {
		return domain.Request{}, domain.ErrInvalid
	}
	revision, err := ReadRevision(r.Expected)
	if err != nil {
		return domain.Request{}, err
	}
	result := domain.Request{InstanceID: r.InstanceId, OperationID: r.OperationId, Expected: revision, Selection: r.Selection}
	switch r.Mode {
	case v1.Mode_DELTA:
		result.Mode = domain.Delta
	case v1.Mode_FULL:
		result.Mode = domain.Full
	default:
		return domain.Request{}, domain.ErrInvalid
	}
	for _, set := range r.Sets {
		if set == nil || set.MembershipPresent == nil || !*set.MembershipPresent {
			return domain.Request{}, domain.ErrInvalid
		}
		users := set.Accounts
		if users == nil {
			users = [][]byte{}
		}
		result.Sets = append(result.Sets, accounts.InboundUserSet{InboundTag: set.InboundTag, Accounts: users})
	}
	for _, item := range r.Removals {
		if item == nil {
			return domain.Request{}, domain.ErrInvalid
		}
		result.Removals = append(result.Removals, accounts.Removal{InboundTag: item.InboundTag, Email: item.Email})
	}
	if err := ctx.Err(); err != nil {
		return domain.Request{}, err
	}
	return result, nil
}
func Preview(p domain.Preview) *v1.Preview {
	r := &v1.Preview{PlanId: p.PlanID, ConfigOperationId: p.ConfigOperationID, InstanceId: p.InstanceID, OperationId: p.OperationID, Revision: Revision(p.Revision), ApplyMode: p.ApplyMode, Reason: p.Reason, ObservationBasis: p.ObservationBasis, ExpiresAt: p.ExpiresAt.Format(time.RFC3339Nano)}
	if p.Mode == domain.Full {
		r.Mode = v1.Mode_FULL
	} else {
		r.Mode = v1.Mode_DELTA
	}
	for _, d := range p.Differences {
		r.Differences = append(r.Differences, &v1.Difference{InboundTag: d.InboundTag, Added: uint32(d.Added), Removed: uint32(d.Removed), Replaced: uint32(d.Replaced), Unchanged: uint32(d.Unchanged)})
	}
	return r
}
func Result(p domain.ReceiptPage, err error) *v1.Result {
	r := p.Receipt
	result := &v1.Result{InstanceId: r.InstanceID, OperationId: r.OperationID, ConfigOperationId: r.ConfigOperationID, Revision: Revision(r.Revision), Effect: string(r.Effect), InFlight: r.InFlight, Durable: r.Durable, Ready: r.Ready, RequiresRecovery: r.RequiresRecovery, NextOffset: uint32(p.NextOffset)}
	if !r.CompletedAt.IsZero() {
		result.CompletedAt = r.CompletedAt.Format(time.RFC3339Nano)
	}
	if err != nil {
		result.Error = Code(err)
	}
	for _, step := range r.Steps {
		result.Steps = append(result.Steps, &v1.Step{InboundTag: step.InboundTag, Email: step.Email, Action: string(step.Action), Attempted: step.Attempted, Effect: string(step.Effect), Acknowledged: step.Acknowledged, RequiresRestart: step.RequiresRestart, Error: step.Code})
	}
	return result
}
func Transfer(t services.TransferStatus) *v1.Transfer {
	return &v1.Transfer{Id: t.ID, InstanceId: t.InstanceID, NextSequence: t.NextSequence, Final: t.Final, ExpiresAt: t.ExpiresAt.Format(time.RFC3339Nano)}
}
func Chunk(r *v1.ChunkRequest) (services.Chunk, error) {
	if r == nil {
		return services.Chunk{}, domain.ErrInvalid
	}
	c := services.Chunk{Sequence: r.Sequence, Final: r.Final}
	if len(r.Additions)+len(r.Removals) > domain.MaxChunkItems {
		return c, domain.ErrCapacity
	}
	for _, a := range r.Additions {
		if a == nil {
			return c, domain.ErrInvalid
		}
		c.Additions = append(c.Additions, accounts.Addition{InboundTag: a.InboundTag, Configuration: a.Configuration})
	}
	for _, r := range r.Removals {
		if r == nil {
			return c, domain.ErrInvalid
		}
		c.Removals = append(c.Removals, accounts.Removal{InboundTag: r.InboundTag, Email: r.Email})
	}
	return c, nil
}
