package grpcapi_test

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/grpcapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAbsentMessagesNeverReachApplication(t *testing.T) {
	handler := grpcapi.Handler{}
	ctx := context.Background()
	_, err := handler.ListInbounds(ctx, nil)
	checkInvalid(t, err)
	_, err = handler.ListInboundTags(ctx, nil)
	checkInvalid(t, err)
	_, err = handler.GetInboundControlStatus(ctx, nil)
	checkInvalid(t, err)
	_, err = handler.AddInbound(ctx, nil)
	checkInvalid(t, err)
	_, err = handler.RemoveInbound(ctx, nil)
	checkInvalid(t, err)
}
func checkInvalid(t *testing.T, err error) {
	t.Helper()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
