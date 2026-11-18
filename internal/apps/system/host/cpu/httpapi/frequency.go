package httpapi

import (
	"context"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/gofiber/fiber/v3"
)

func (handler Handler) ReadFrequency(request fiber.Ctx) error {
	request.Set(fiber.HeaderCacheControl, "no-store")
	if len(request.Body()) != 0 || len(request.Request().URI().QueryString()) != 0 {
		return request.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid_argument"})
	}
	ctx, cancel := context.WithTimeout(request.Context(), handler.Timeout)
	defer cancel()
	result, err := handler.Service.ReadFrequency(ctx)
	if err != nil {
		return readError(request, err)
	}
	return request.JSON(frequencyResult(result))
}

type frequencyAttribute struct {
	Attribute string `json:"attribute"`
	Reason    string `json:"reason"`
}
type frequencyPolicy struct {
	ID                  uint32               `json:"id"`
	RelatedCPUs         []uint32             `json:"related_cpus"`
	AffectedCPUs        []uint32             `json:"affected_cpus"`
	HardwareCurrentKHz  *uint64              `json:"hardware_current_khz,string"`
	HardwareAverageKHz  *uint64              `json:"hardware_average_khz,string"`
	HardwareMinKHz      *uint64              `json:"hardware_min_khz,string"`
	HardwareMaxKHz      *uint64              `json:"hardware_max_khz,string"`
	ScalingCurrentKHz   *uint64              `json:"scaling_current_khz,string"`
	ScalingMinKHz       *uint64              `json:"scaling_min_khz,string"`
	ScalingMaxKHz       *uint64              `json:"scaling_max_khz,string"`
	TransitionLatencyNS *uint64              `json:"transition_latency_ns,string"`
	Driver              *string              `json:"driver"`
	Governor            *string              `json:"governor"`
	AvailableGovernors  []string             `json:"available_governors"`
	AvailableKHz        []string             `json:"available_khz"`
	Unavailable         []frequencyAttribute `json:"unavailable"`
}
type frequencyResponse struct {
	InterfacePresent bool              `json:"interface_present"`
	Policies         []frequencyPolicy `json:"policies"`
	ObservedAt       time.Time         `json:"observed_at"`
	DurationNS       int64             `json:"duration_ns,string"`
}

func frequencyResult(result domain.FrequencyObservation) frequencyResponse {
	rows := make([]frequencyPolicy, 0, len(result.Frequencies.Policies))
	for _, row := range result.Frequencies.Policies {
		available := make([]string, 0, len(row.AvailableKHz))
		for _, value := range row.AvailableKHz {
			available = append(available, strconv.FormatUint(value, 10))
		}
		issues := make([]frequencyAttribute, 0, len(row.Unavailable))
		for _, issue := range row.Unavailable {
			issues = append(issues, frequencyAttribute{Attribute: issue.Attribute, Reason: issue.Reason})
		}
		rows = append(rows, frequencyPolicy{
			ID:                  row.ID,
			RelatedCPUs:         row.RelatedCPUs,
			AffectedCPUs:        row.AffectedCPUs,
			HardwareCurrentKHz:  row.HardwareCurrentKHz,
			HardwareAverageKHz:  row.HardwareAverageKHz,
			HardwareMinKHz:      row.HardwareMinKHz,
			HardwareMaxKHz:      row.HardwareMaxKHz,
			ScalingCurrentKHz:   row.ScalingCurrentKHz,
			ScalingMinKHz:       row.ScalingMinKHz,
			ScalingMaxKHz:       row.ScalingMaxKHz,
			TransitionLatencyNS: row.TransitionLatencyNS,
			Driver:              row.Driver,
			Governor:            row.Governor,
			AvailableGovernors:  row.AvailableGovernors,
			AvailableKHz:        available,
			Unavailable:         issues,
		})
	}
	return frequencyResponse{InterfacePresent: result.Frequencies.InterfacePresent, Policies: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
