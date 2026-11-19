// Package grpcapi adapts physical sensor observations to gRPC.
package grpcapi

import (
	"context"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	sensorsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sensors/grpcapi/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	sensorsv1.UnimplementedHostSensorsServiceServer
	Service services.Service
	Timeout time.Duration
}

func (h Handler) Register(s grpc.ServiceRegistrar) { sensorsv1.RegisterHostSensorsServiceServer(s, h) }
func (h Handler) GetTemperatures(ctx context.Context, _ *sensorsv1.GetTemperaturesRequest) (*sensorsv1.GetTemperaturesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Temperatures(ctx)
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*sensorsv1.Temperature, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, &sensorsv1.Temperature{Id: row.ID, Device: row.Device, Label: row.Label, MilliCelsius: row.MilliCelsius, HighMilliCelsius: row.HighMilliCelsius, CriticalMilliCelsius: row.CriticalMilliCelsius})
	}
	return &sensorsv1.GetTemperaturesResponse{Readings: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) GetFans(ctx context.Context, _ *sensorsv1.GetFansRequest) (*sensorsv1.GetFansResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.Fans(ctx)
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*sensorsv1.Fan, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, &sensorsv1.Fan{Id: row.ID, Device: row.Device, Label: row.Label, Rpm: row.RPM})
	}
	return &sensorsv1.GetFansResponse{Readings: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func (h Handler) GetPowerSupplies(ctx context.Context, _ *sensorsv1.GetPowerSuppliesRequest) (*sensorsv1.GetPowerSuppliesResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	result, err := h.Service.PowerSupplies(ctx)
	if err != nil {
		return nil, readError(err)
	}
	rows := make([]*sensorsv1.PowerSupply, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, &sensorsv1.PowerSupply{Id: row.ID, Type: row.Type, Status: row.Status, Present: row.Present, Online: row.Online, CapacityPercent: row.CapacityPercent, EnergyNowMicroWh: row.EnergyNowMicroWh, EnergyFullMicroWh: row.EnergyFullMicroWh, ChargeNowMicroAh: row.ChargeNowMicroAh, ChargeFullMicroAh: row.ChargeFullMicroAh, PowerMicroW: row.PowerMicroW, CurrentMicroA: row.CurrentMicroA, VoltageMicroV: row.VoltageMicroV, TimeToEmptySeconds: row.TimeToEmptySeconds, TimeToFullSeconds: row.TimeToFullSeconds})
	}
	return &sensorsv1.GetPowerSuppliesResponse{Readings: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
func readError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return status.FromContextError(err).Err()
	}
	return status.Error(codes.Unavailable, "sensor observation unavailable")
}
