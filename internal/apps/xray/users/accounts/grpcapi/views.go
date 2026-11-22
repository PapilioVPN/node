package grpcapi

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func accountMetadata(m domain.Metadata) *v1.AccountMetadata {
	return &v1.AccountMetadata{Email: m.Email, Level: m.Level, AccountType: m.AccountType}
}
func readCost(c domain.ReadCost) *v1.AccountReadCost {
	return &v1.AccountReadCost{NativePayloadBytes: uint64(c.NativeBytes), NativeDurationNs: int64(c.NativeDuration), NativeLimitBytes: domain.MaxCollectionBytes}
}
func operationGuidance(g *domain.Guidance) *v1.AccountGuidance {
	if g == nil {
		return nil
	}
	return &v1.AccountGuidance{Reason: g.Reason, NextAction: g.NextAction, Protocol: g.Protocol, ExpectedAccountType: g.ExpectedAccountType, ActualAccountType: g.ActualAccountType}
}
func (h Handler) ListAccountMetadata(ctx context.Context, r *v1.ListAccountMetadataRequest) (*v1.AccountMetadataPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.ListMetadata(ctx, r.InstanceId, r.InboundTag, domain.PageRequest{Size: r.PageSize, Cursor: r.Cursor})
	if err != nil {
		return nil, failure(err)
	}
	users := make([]*v1.AccountMetadata, len(result.Users))
	for i, m := range result.Users {
		users[i] = accountMetadata(m)
	}
	return &v1.AccountMetadataPage{Observation: metadata(result.Observation), Users: users, NextCursor: result.NextCursor, ExpiresAt: timestamppb.New(result.ExpiresAt), ReadCost: readCost(result.ReadCost)}, nil
}
func (h Handler) ExportAccounts(ctx context.Context, r *v1.ExportAccountsRequest) (*v1.AccountExportPage, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Export(ctx, r.InstanceId, r.InboundTag, domain.PageRequest{Size: r.PageSize, Cursor: r.Cursor})
	if err != nil {
		return nil, failure(err)
	}
	users := make([]*v1.Account, len(result.Users))
	for i, m := range result.Users {
		users[i] = &v1.Account{Email: m.Email, Level: m.Level, Configuration: m.Configuration}
	}
	return &v1.AccountExportPage{Observation: metadata(result.Observation), Users: users, NextCursor: result.NextCursor, ExpiresAt: timestamppb.New(result.ExpiresAt), ReadCost: readCost(result.ReadCost)}, nil
}
func (h Handler) GetAccountMetadata(ctx context.Context, r *v1.GetAccountRequest) (*v1.AccountMetadataObservation, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.GetMetadata(ctx, r.InstanceId, r.InboundTag, r.Email)
	if err != nil {
		return nil, failure(err)
	}
	response := &v1.AccountMetadataObservation{Observation: metadata(result.Observation), RequestedEmail: result.RequestedEmail}
	if result.User != nil {
		response.User = accountMetadata(*result.User)
	}
	return response, nil
}
func (h Handler) CheckAccountCompatibility(ctx context.Context, r *v1.CheckAccountCompatibilityRequest) (*v1.AccountCompatibility, error) {
	if r == nil {
		return nil, failure(domain.ErrInvalidArgument)
	}
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.CheckCompatibility(ctx, r.InstanceId, r.InboundTag, r.AccountType)
	if err != nil {
		return nil, failure(err)
	}
	return &v1.AccountCompatibility{Observation: metadata(result.Observation), Protocol: result.Inbound.Protocol, ExpectedAccountType: result.Inbound.AccountType, LiveAccounts: result.Inbound.LiveAccounts, EmailMatching: result.Inbound.EmailMatching, RequestedAccountType: result.RequestedAccountType, Compatible: result.Compatible, Guidance: operationGuidance(&result.Guidance)}, nil
}
