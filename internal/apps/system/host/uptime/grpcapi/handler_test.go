package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/uptime/grpcapi"
	uptimev1 "github.com/PapilioVPN/node/internal/apps/system/host/uptime/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReadPreservesCancellationAndSanitizesNativeFailure(t *testing.T) {
	for _, test := range []struct {
		cause error
		code  codes.Code
	}{
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{errors.New("private-native-cause"), codes.Unavailable},
	} {
		handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Reader: readerFunc(func(context.Context) (time.Duration, error) { return 0, test.cause })}}
		result, err := handler.GetHostUptime(context.Background(), &uptimev1.GetHostUptimeRequest{})
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private-native-cause") {
			t.Fatalf("invalid failure: %v %v", result, err)
		}
	}
}
