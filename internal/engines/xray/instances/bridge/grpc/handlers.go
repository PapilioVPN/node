package grpc

import (
	"context"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
)

type handlers struct {
	instancesv1.UnimplementedXrayInstancesServiceServer
	service *lifecycle.Service
}

func (h *handlers) GetStatus(_ context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	return response(h.service.GetStatus(req.GetId()))
}

func (h *handlers) Start(ctx context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	groups := req.GetInitialUsers()
	if len(groups) > lifecycle.MaxInitialInbounds {
		return h.invalidStart(req.GetId())
	}
	var users []lifecycle.InboundUsers
	var total int
	for _, group := range groups {
		total += len(group.GetAccounts())
		if total > membership.MaxSyncAccounts {
			return h.invalidStart(req.GetId())
		}
		accounts := make([]membership.AccountInput, 0, len(group.GetAccounts()))
		for _, account := range group.GetAccounts() {
			id := account.GetUuid()
			accounts = append(accounts, membership.AccountInput{Email: account.GetEmail(), UUID: &id})
		}
		users = append(users, lifecycle.InboundUsers{InboundTag: group.GetInboundTag(), Accounts: accounts})
	}
	input := lifecycle.StartRequest{ID: req.GetId(), ConfigJSON: req.GetConfigJson(),
		InitialUsers: users, ExcludedInbounds: req.GetExcludeInbounds()}
	if req != nil {
		input.KeepAliveSeconds = req.KeepAliveSeconds
	}
	return response(h.service.StartWith(ctx, input))
}

func (h *handlers) invalidStart(id string) (*instancesv1.XrayInstanceStatus, error) {
	observed, err := h.service.GetStatus(id)
	if err != nil {
		return response(observed, err)
	}
	return response(observed, lifecycle.ErrInvalidStart)
}

func (h *handlers) Stop(ctx context.Context, req *instancesv1.XrayInstanceRequest) (*instancesv1.XrayInstanceStatus, error) {
	return response(h.service.Stop(ctx, req.GetId()))
}
