package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/load/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/load/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/load/grpcapi"
	loadv1 "github.com/PapilioVPN/node/internal/apps/system/host/load/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReadMapsDistinctLoadWindowsAndZeroWithoutExtraReads(t *testing.T) {
	calls := 0
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) {
		calls++
		return domain.Averages{OneMinute: 0, FiveMinutes: 1.5, FifteenMinutes: 256.25}, nil
	})}}
	result, err := handler.GetHostLoad(context.Background(), &loadv1.GetHostLoadRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || result.OneMinute != 0 || result.FiveMinutes != 1.5 || result.FifteenMinutes != 256.25 || result.ObservedAt.CheckValid() != nil || result.DurationNs < 0 {
		t.Fatalf("invalid gRPC load: %+v; reads=%d", result, calls)
	}
}

func TestReadPreservesCancellationAndSanitizesNativeFailure(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{errors.New("private-native-cause"), codes.Unavailable},
	} {
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (domain.Averages, error) { return domain.Averages{}, test.cause })}}
		result, err := handler.GetHostLoad(context.Background(), &loadv1.GetHostLoadRequest{})
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-native-cause") {
			t.Fatalf("invalid failure: %v %v", result, err)
		}
	}
}
