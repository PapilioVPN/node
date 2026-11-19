// Package domain defines Node's own runtime observations, separate from host facts.
package domain

import "time"

type Info struct {
	GoVersion       string
	Compiler        string
	OperatingSystem string
	Architecture    string
	Executable      string
	PID             int32
	ParentPID       int32
	InitializedAt   time.Time
	ModulePath      string
	ModuleVersion   string
	Revision        *string
	RevisionTime    *string
	Modified        *bool
}
type Resources struct {
	PID                int32
	StartTicks         uint64
	TicksPerSecond     uint64
	UserTicks          uint64
	SystemTicks        uint64
	VirtualBytes       uint64
	ResidentBytes      uint64
	Threads            uint64
	FileDescriptors    uint64
	Goroutines         uint64
	GoMaxProcs         uint64
	RuntimeMappedBytes uint64
	HeapObjectsBytes   uint64
	HeapReleasedBytes  uint64
	AllocatedBytes     uint64
	FreedBytes         uint64
	GCCycles           uint64
}
type InfoObservation struct {
	Info       Info
	ObservedAt time.Time
	Duration   time.Duration
}
type ResourceObservation struct {
	Resources  Resources
	ObservedAt time.Time
	Duration   time.Duration
}
