package grpcapi_test

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
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
