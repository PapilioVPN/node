package grpcapi_test

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMissingMessageNeverReachesService(t *testing.T) {
	h := grpcapi.Handler{}
	if _, err := h.GetAccountCount(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.GetAccount(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestMissingControlInputsNeverReachService(t *testing.T) {
	h := grpcapi.Handler{}
	if _, err := h.AddAccount(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.RemoveAccount(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.RemoveAccount(context.Background(), &v1.RemoveAccountRequest{InstanceId: "a", InboundTag: "b"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.GetAccountControlStatus(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestMissingSourceInputsNeverReachService(t *testing.T) {
	h := grpcapi.Handler{}
	if _, err := h.AddAccountFromSources(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.AddAccountFromSources(context.Background(), &v1.AddAccountFromSourcesRequest{InstanceId: "a"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestInvalidBatchMessagesNeverReachService(t *testing.T) {
	h := grpcapi.Handler{}
	for _, request := range []*v1.AddAccountsRequest{nil, {Parallelism: 5}, {Items: []*v1.AccountAddition{nil}}} {
		if _, err := h.AddAccounts(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	for _, request := range []*v1.RemoveAccountsRequest{nil, {Parallelism: 5}, {Items: []*v1.AccountRemoval{nil}}, {Items: []*v1.AccountRemoval{{InboundTag: "members"}}}} {
		if _, err := h.RemoveAccounts(context.Background(), request); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
}
