package grpcapi

import (
	"context"
	"errors"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (handler Handler) GetHostCPUFrequency(ctx context.Context, _ *cpuv1.GetHostCPUFrequencyRequest) (*cpuv1.GetHostCPUFrequencyResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadFrequency(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Unavailable, "host CPU frequency unavailable")
	}
	rows := make([]*cpuv1.FrequencyPolicy, 0, len(result.Frequencies.Policies))
	for _, row := range result.Frequencies.Policies {
		issues := make([]*cpuv1.FrequencyAttributeUnavailable, 0, len(row.Unavailable))
		for _, issue := range row.Unavailable {
			issues = append(issues, &cpuv1.FrequencyAttributeUnavailable{Attribute: issue.Attribute, Reason: issue.Reason})
		}
		rows = append(rows, &cpuv1.FrequencyPolicy{
			Id:                  row.ID,
			RelatedCpus:         row.RelatedCPUs,
			AffectedCpus:        row.AffectedCPUs,
			HardwareCurrentKhz:  row.HardwareCurrentKHz,
			HardwareAverageKhz:  row.HardwareAverageKHz,
			HardwareMinKhz:      row.HardwareMinKHz,
			HardwareMaxKhz:      row.HardwareMaxKHz,
			ScalingCurrentKhz:   row.ScalingCurrentKHz,
			ScalingMinKhz:       row.ScalingMinKHz,
			ScalingMaxKhz:       row.ScalingMaxKHz,
			TransitionLatencyNs: row.TransitionLatencyNS,
			Driver:              row.Driver,
			Governor:            row.Governor,
			AvailableGovernors:  row.AvailableGovernors,
			AvailableKhz:        row.AvailableKHz,
			Unavailable:         issues,
		})
	}
	return &cpuv1.GetHostCPUFrequencyResponse{InterfacePresent: result.Frequencies.InterfacePresent, Policies: rows, ObservedAt: timestamppb.New(result.ObservedAt), DurationNs: int64(result.Duration)}, nil
}
