package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi"
	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGRPCActivityRetainsCountsAndSanitizesCauses(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{ActivityReader: activityReaderFunc(func(context.Context) (domain.Activity, error) { return sampleActivity(), nil })}}
	got, err := handler.GetHostCPUActivity(context.Background(), &cpuv1.GetHostCPUActivityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextSwitches != 9007199254740993 || got.Interrupts != 1<<64-1 || got.SoftInterrupts != 200 || got.CreatedTasks != 300 || got.RunnableTasks != 0 || got.IoBlockedTasks != 2 || got.ObservedAt.CheckValid() != nil || got.DurationNs < 0 {
		t.Fatalf("lost activity: %v", got)
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{errors.New("private source"), codes.Unavailable}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}} {
		handler.Service.ActivityReader = activityReaderFunc(func(context.Context) (domain.Activity, error) { return sampleActivity(), test.cause })
		got, err := handler.GetHostCPUActivity(context.Background(), &cpuv1.GetHostCPUActivityRequest{})
		if got != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe failure: %v %v", got, err)
		}
	}
}
