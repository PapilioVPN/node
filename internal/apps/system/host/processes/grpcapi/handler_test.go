package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/processes/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/grpcapi"
	processesv1 "github.com/PapilioVPN/node/internal/apps/system/host/processes/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProcessGRPCPreservesSelectionPrecisionAndPartialResults(t *testing.T) {
	reader := &readerFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader, TicksPerSecond: 100}}
	count, err := handler.CountProcesses(context.Background(), &processesv1.CountProcessesRequest{})
	if err != nil || count.Count != 9007199254740993 || count.ObservedAt.CheckValid() != nil {
		t.Fatalf("%v %v", count, err)
	}
	process, err := handler.GetProcess(context.Background(), &processesv1.GetProcessRequest{Pid: 42})
	if err != nil || process.Process.UserTicks != 18446744073709551615 || process.Process.StartTicks != 9007199254740993 || process.TicksPerSecond != 100 || reader.pid != 42 {
		t.Fatalf("%v %v", process, err)
	}
	page, err := handler.ListProcesses(context.Background(), &processesv1.ListProcessesRequest{AfterPid: 41, Limit: 1})
	if err != nil || len(page.Entries) != 2 || page.Entries[1].Process != nil || page.Entries[1].Issue != "permission_denied" || page.NextAfterPid == nil || *page.NextAfterPid != 43 || reader.selection.AfterPID != 41 || reader.selection.Limit != 1 || reader.calls != 3 {
		t.Fatalf("%v %v", page, err)
	}
}

func TestProcessGRPCRejectsInvalidSelectionAndMapsErrors(t *testing.T) {
	reader := &readerFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: reader}}
	if _, err := handler.GetProcess(context.Background(), &processesv1.GetProcessRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.ListProcesses(context.Background(), &processesv1.ListProcessesRequest{Limit: 257}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if reader.calls != 0 {
		t.Fatal("invalid input reached native I/O")
	}
	for _, failure := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrNotFound, codes.NotFound}, {domain.ErrPermissionDenied, codes.PermissionDenied}, {domain.ErrResourceExhausted, codes.ResourceExhausted}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private path"), codes.Unavailable}} {
		reader.cause = failure.cause
		if result, err := handler.GetProcess(context.Background(), &processesv1.GetProcessRequest{Pid: 42}); result != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("%v %v", result, err)
		}
	}
}
