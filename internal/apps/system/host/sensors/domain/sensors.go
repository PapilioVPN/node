// Package domain defines host-visible physical sensor observations.
package domain

import "time"

type Temperature struct {
	ID                   string
	Device               string
	Label                *string
	MilliCelsius         int64
	HighMilliCelsius     *int64
	CriticalMilliCelsius *int64
}
type Fan struct {
	ID     string
	Device string
	Label  *string
	RPM    uint64
}

// PowerSupply retains native units and property presence, without an inferred
// battery model or conflating charge (microAh) with energy (microWh).
type PowerSupply struct {
	ID                 string
	Type               string
	Status             *string
	Present            *bool
	Online             *bool
	CapacityPercent    *uint32
	EnergyNowMicroWh   *uint64
	EnergyFullMicroWh  *uint64
	ChargeNowMicroAh   *uint64
	ChargeFullMicroAh  *uint64
	PowerMicroW        *int64
	CurrentMicroA      *int64
	VoltageMicroV      *uint64
	TimeToEmptySeconds *uint64
	TimeToFullSeconds  *uint64
}
type Temperatures struct {
	Readings   []Temperature
	ObservedAt time.Time
	Duration   time.Duration
}
type Fans struct {
	Readings   []Fan
	ObservedAt time.Time
	Duration   time.Duration
}
type PowerSupplies struct {
	Readings   []PowerSupply
	ObservedAt time.Time
	Duration   time.Duration
}
