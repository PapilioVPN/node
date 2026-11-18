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

func TestGRPCFrequencyPreservesPresenceUnitsAndIssues(t *testing.T) {
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{FrequencyReader: frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) { return sampleFrequencies(), nil })}}
	got, err := handler.GetHostCPUFrequency(context.Background(), &cpuv1.GetHostCPUFrequencyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.InterfacePresent || len(got.Policies) != 1 || got.ObservedAt.CheckValid() != nil || got.DurationNs < 0 {
		t.Fatalf("invalid observation: %v", got)
	}
	row := got.Policies[0]
	if row.Id != 2 || len(row.RelatedCpus) != 2 || row.RelatedCpus[1] != 3 || row.ScalingCurrentKhz == nil || *row.ScalingCurrentKhz != 9007199254740993 || row.HardwareMinKhz == nil || *row.HardwareMinKhz != 0 || row.HardwareCurrentKhz != nil || len(row.AvailableKhz) != 1 || row.AvailableKhz[0] != 9007199254740993 || len(row.Unavailable) != 1 || row.Unavailable[0].Attribute != "cpuinfo_cur_freq" || row.Unavailable[0].Reason != "permission_denied" {
		t.Fatalf("lost precision/presence: %v", row)
	}
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{{errors.New("private sysfs path"), codes.Unavailable}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}} {
		handler.Service.FrequencyReader = frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) { return sampleFrequencies(), test.cause })
		got, err := handler.GetHostCPUFrequency(context.Background(), &cpuv1.GetHostCPUFrequencyRequest{})
		if got != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe failure: %v %v", got, err)
		}
	}
}
