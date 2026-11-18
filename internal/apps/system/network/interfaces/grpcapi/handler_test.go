package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/network/interfaces/grpcapi"
	interfacesv1 "github.com/PapilioVPN/node/internal/apps/system/network/interfaces/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCInterfacesPreserveAddressesAndCounterPresence(t *testing.T) {
	reader := &readerFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	got, err := handler.ListInterfaces(context.Background(), &interfacesv1.ListInterfacesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Interfaces) != 1 || got.Interfaces[0].Index != 42 || got.Interfaces[0].ParentNamespaceId == nil || *got.Interfaces[0].ParentNamespaceId != 0 || got.Interfaces[0].Flags != 0x80000001 || len(got.Interfaces[0].Addresses) != 1 || got.Interfaces[0].Addresses[0].GetPeerPrefix() != "198.51.100.5/24" || got.Interfaces[0].Addresses[0].ValidLifetimeSeconds != 1<<32-1 || len(got.UnmatchedAddresses) != 1 || got.UnmatchedAddresses[0].InterfaceIndex != 77 || got.ObservedAt.CheckValid() != nil || got.DurationNs < 0 {
		t.Fatalf("lost address facts: %v", got)
	}
	counters, err := handler.GetInterfaceCounters(context.Background(), &interfacesv1.GetInterfaceCountersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(counters.Interfaces) != 2 || counters.Interfaces[0].Counters == nil || counters.Interfaces[0].Counters.ReceiveBytes != 9007199254740993 || counters.Interfaces[0].Counters.TransmitBytes != 1<<64-1 || counters.Interfaces[0].Counters.TransmitCarrierErrors != 11 || counters.Interfaces[1].Counters != nil || reader.calls != 1 || reader.counterCalls != 1 {
		t.Fatalf("lost counters: %v", counters)
	}
	for _, failure := range []struct {
		cause error
		code  codes.Code
	}{{errors.New("private socket"), codes.Unavailable}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}} {
		handler.Service.Reader = &readerFixture{cause: failure.cause}
		if got, err := handler.ListInterfaces(context.Background(), &interfacesv1.ListInterfacesRequest{}); got != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe inventory failure: %v %v", got, err)
		}
		if got, err := handler.GetInterfaceCounters(context.Background(), &interfacesv1.GetInterfaceCountersRequest{}); got != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe counter failure: %v %v", got, err)
		}
	}
}
