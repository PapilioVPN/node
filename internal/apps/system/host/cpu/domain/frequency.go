package domain

import "time"

// FrequencyPolicy is a Linux CPUFreq policy shared by its related CPUs.
// Frequencies are exact kHz, not MHz. Missing attributes retain absence.
type FrequencyPolicy struct {
	ID                  uint32
	RelatedCPUs         []uint32
	AffectedCPUs        []uint32
	HardwareCurrentKHz  *uint64
	HardwareAverageKHz  *uint64
	HardwareMinKHz      *uint64
	HardwareMaxKHz      *uint64
	ScalingCurrentKHz   *uint64
	ScalingMinKHz       *uint64
	ScalingMaxKHz       *uint64
	TransitionLatencyNS *uint64
	Driver              *string
	Governor            *string
	AvailableGovernors  []string
	AvailableKHz        []uint64
	Unavailable         []FrequencyAttribute
}

// FrequencyAttribute records why a policy attribute could not be observed.
// Attribute uses the native ABI name; no private filesystem path is exposed.
type FrequencyAttribute struct {
	Attribute string
	Reason    string // not_present, permission_denied or temporarily_unavailable
}

type Frequencies struct {
	InterfacePresent bool
	Policies         []FrequencyPolicy
}

type FrequencyObservation struct {
	Frequencies Frequencies
	ObservedAt  time.Time
	Duration    time.Duration
}
