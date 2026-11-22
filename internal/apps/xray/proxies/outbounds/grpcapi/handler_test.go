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

func TestMissingControlMessagesNeverReachService(t *testing.T) {
	h := grpcapi.Handler{}
	ctx := context.Background()
	_, err := h.AddOutbound(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	_, err = h.RemoveOutbound(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	_, err = h.AddOutboundFromSources(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	_, err = h.GetOutboundControlStatus(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

func TestMissingReplacementMessagesNeverReachService(t *testing.T) {
	h := grpcapi.Handler{}
	ctx := context.Background()
	if _, err := h.PlanOutboundReplacement(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.ApplyOutboundReplacement(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.GetOutboundReplacement(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
