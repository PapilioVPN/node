package natsapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
)

type temperaturesRow struct {
	ID                   string  `json:"id"`
	Device               string  `json:"device"`
	Label                *string `json:"label"`
	MilliCelsius         int64   `json:"milli_celsius,string"`
	HighMilliCelsius     *int64  `json:"high_milli_celsius,string"`
	CriticalMilliCelsius *int64  `json:"critical_milli_celsius,string"`
}
type temperaturesResponse struct {
	Readings   []temperaturesRow `json:"readings"`
	ObservedAt time.Time         `json:"observed_at"`
	DurationNS int64             `json:"duration_ns,string"`
}

func temperaturesResult(result domain.Temperatures) temperaturesResponse {
	rows := make([]temperaturesRow, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, temperaturesRow{ID: row.ID, Device: row.Device, Label: row.Label, MilliCelsius: row.MilliCelsius, HighMilliCelsius: row.HighMilliCelsius, CriticalMilliCelsius: row.CriticalMilliCelsius})
	}
	return temperaturesResponse{Readings: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type fansRow struct {
	ID     string  `json:"id"`
	Device string  `json:"device"`
	Label  *string `json:"label"`
	RPM    uint64  `json:"rpm,string"`
}
type fansResponse struct {
	Readings   []fansRow `json:"readings"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
}

func fansResult(result domain.Fans) fansResponse {
	rows := make([]fansRow, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, fansRow{ID: row.ID, Device: row.Device, Label: row.Label, RPM: row.RPM})
	}
	return fansResponse{Readings: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type powerSuppliesRow struct {
	ID                 string  `json:"id"`
	Type               string  `json:"type"`
	Status             *string `json:"status"`
	Present            *bool   `json:"present"`
	Online             *bool   `json:"online"`
	CapacityPercent    *uint32 `json:"capacity_percent"`
	EnergyNowMicroWh   *uint64 `json:"energy_now_micro_wh,string"`
	EnergyFullMicroWh  *uint64 `json:"energy_full_micro_wh,string"`
	ChargeNowMicroAh   *uint64 `json:"charge_now_micro_ah,string"`
	ChargeFullMicroAh  *uint64 `json:"charge_full_micro_ah,string"`
	PowerMicroW        *int64  `json:"power_micro_w,string"`
	CurrentMicroA      *int64  `json:"current_micro_a,string"`
	VoltageMicroV      *uint64 `json:"voltage_micro_v,string"`
	TimeToEmptySeconds *uint64 `json:"time_to_empty_seconds,string"`
	TimeToFullSeconds  *uint64 `json:"time_to_full_seconds,string"`
}
type powerSuppliesResponse struct {
	Readings   []powerSuppliesRow `json:"readings"`
	ObservedAt time.Time          `json:"observed_at"`
	DurationNS int64              `json:"duration_ns,string"`
}

func powerSuppliesResult(result domain.PowerSupplies) powerSuppliesResponse {
	rows := make([]powerSuppliesRow, 0, len(result.Readings))
	for _, row := range result.Readings {
		rows = append(rows, powerSuppliesRow{ID: row.ID, Type: row.Type, Status: row.Status, Present: row.Present, Online: row.Online, CapacityPercent: row.CapacityPercent, EnergyNowMicroWh: row.EnergyNowMicroWh, EnergyFullMicroWh: row.EnergyFullMicroWh, ChargeNowMicroAh: row.ChargeNowMicroAh, ChargeFullMicroAh: row.ChargeFullMicroAh, PowerMicroW: row.PowerMicroW, CurrentMicroA: row.CurrentMicroA, VoltageMicroV: row.VoltageMicroV, TimeToEmptySeconds: row.TimeToEmptySeconds, TimeToFullSeconds: row.TimeToFullSeconds})
	}
	return powerSuppliesResponse{Readings: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
