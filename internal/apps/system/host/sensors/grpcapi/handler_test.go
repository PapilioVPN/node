package grpcapi_test

import (
	"context"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/grpcapi"
	sensorsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sensors/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSensorNativeTypesAndCancellation(t *testing.T) {
	r := &reader{}
	h := grpcapi.Handler{Service: services.Service{Reader: r}, Timeout: time.Second}
	ctx := context.Background()
	temperatures, err := h.GetTemperatures(ctx, &sensorsv1.GetTemperaturesRequest{})
	if err != nil || len(temperatures.Readings) != 1 || temperatures.Readings[0].MilliCelsius != -12500 || temperatures.Readings[0].HighMilliCelsius == nil || *temperatures.Readings[0].HighMilliCelsius != 0 || temperatures.Readings[0].CriticalMilliCelsius != nil {
		t.Fatalf("%+v %v", temperatures, err)
	}
	fans, err := h.GetFans(ctx, &sensorsv1.GetFansRequest{})
	if err != nil || fans.Readings[0].Rpm != 9007199254740993 {
		t.Fatalf("%+v %v", fans, err)
	}
	power, err := h.GetPowerSupplies(ctx, &sensorsv1.GetPowerSuppliesRequest{})
	if err != nil || power.Readings[0].Present == nil || *power.Readings[0].Present || power.Readings[0].Online != nil {
		t.Fatalf("%+v %v", power, err)
	}
	r.cause = context.DeadlineExceeded
	if _, err := h.GetFans(ctx, &sensorsv1.GetFansRequest{}); status.Code(err) != codes.DeadlineExceeded {
		t.Fatal(err)
	}
	before := r.calls
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := h.GetFans(ctx, &sensorsv1.GetFansRequest{}); status.Code(err) != codes.Canceled || r.calls != before {
		t.Fatal(err)
	}
}
