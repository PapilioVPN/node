package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/grpcapi"
	devicesv1 "github.com/PapilioVPN/node/internal/apps/system/storage/devices/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeviceGRPCSelectionPresenceAndPrecision(t *testing.T) {
	reader := &readerFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	result, err := handler.ListDevices(context.Background(), &devicesv1.ListDevicesRequest{AfterName: "nvme0n1", Limit: 2})
	if err != nil || len(result.Devices) != 2 || result.Devices[0].ReadSectors != 9007199254740993 || result.Devices[0].Discard != nil || result.Devices[0].Flush != nil || result.Devices[1].Discard == nil || result.Devices[1].Discard.Completed != 0 || result.Devices[1].Flush.Milliseconds != 17 || result.Devices[1].WriteSectors != 18446744073709551615 || result.NextAfterName == nil || *result.NextAfterName != "sdb" || result.SectorSizeBytes != 512 || reader.selection.AfterName != "nvme0n1" || reader.selection.Limit != 2 || result.ObservedAt.CheckValid() != nil {
		t.Fatalf("%v %v", result, err)
	}
	before := reader.calls
	if _, err := handler.ListDevices(context.Background(), &devicesv1.ListDevicesRequest{Limit: 257}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if reader.calls != before {
		t.Fatal("invalid selection reached native read")
	}
	for _, failure := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrResourceExhausted, codes.ResourceExhausted}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private path"), codes.Unavailable}} {
		reader.cause = failure.cause
		if result, err := handler.ListDevices(context.Background(), &devicesv1.ListDevicesRequest{}); result != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("%v %v", result, err)
		}
	}
}
