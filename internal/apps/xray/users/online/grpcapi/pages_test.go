package grpcapi

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/app/services"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPagedRPCsPreserveMetadataAndRejectOversizedSizes(t *testing.T) {
	r := &pageReader{}
	s, _ := services.New([]services.Binding{{ID: "a", Reader: r, Collection: r}}, statusReader{})
	h := Handler{Service: s, Timeout: time.Second}
	if _, err := h.ListOnlineUsers(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := h.ListOnlineRecords(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	for _, size := range []uint32{51, math.MaxUint32} {
		if _, err := h.ListOnlineUsers(context.Background(), &onlinev1.ListOnlineUsersRequest{InstanceId: "a", PageSize: size}); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	first, err := h.ListOnlineRecords(context.Background(), &onlinev1.ListOnlineRecordsRequest{InstanceId: "a"})
	if err != nil || len(first.Records) != 50 || first.Records[0].Ip.LastSeenUnixSeconds != -1 || !first.Page.Fresh || first.Page.NextPageToken == "" {
		t.Fatal(first, err)
	}
	second, err := h.ListOnlineRecords(context.Background(), &onlinev1.ListOnlineRecordsRequest{InstanceId: "a", PageToken: first.Page.NextPageToken})
	if err != nil || len(second.Records) != 1 || second.Page.Fresh || second.Page.ObservationId != first.Page.ObservationId || !second.Observation.ObservedAt.AsTime().Equal(first.Observation.ObservedAt.AsTime()) || r.calls != 1 {
		t.Fatal(second, err, r.calls)
	}
}
