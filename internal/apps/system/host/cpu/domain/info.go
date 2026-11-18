package domain

import "time"

// Processor is one logical CPU reported by the current Linux kernel.
// Optional fields retain absence. Frequency is the cpuinfo reading, not a limit.
type Processor struct {
	ID               uint32
	VendorID         *string
	Family           *string
	Model            *string
	ModelName        *string
	Stepping         *string
	Microcode        *string
	PackageID        *int32
	CoreID           *int32
	ReportedCores    *uint32
	ReportedSiblings *uint32
	ReportedMHz      *float64
	CacheBytes       *uint64
	ReportedFeatures []string
	ReportedBugs     []string
}

type Info struct {
	LogicalCount uint32
	PackageCount *uint32
	// Sum of consistent reported core counts across observed packages.
	// Packages are OS topology identities, not physical socket guarantees.
	PhysicalCoreCount *uint32
	Processors        []Processor
}

type InfoObservation struct {
	Info       Info
	ObservedAt time.Time
	Duration   time.Duration
}
