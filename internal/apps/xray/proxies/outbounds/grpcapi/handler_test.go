package grpcapi_test

import (
	"context"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/outbounds/grpcapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestMissingMessageNeverReachesService(t *testing.T) {
	_, err := (grpcapi.Handler{}).ListOutbounds(context.Background(), nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
