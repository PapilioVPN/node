package httpapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type infoResponse struct {
	GoVersion       string    `json:"go_version"`
	Compiler        string    `json:"compiler"`
	OperatingSystem string    `json:"operating_system"`
	Architecture    string    `json:"architecture"`
	Executable      string    `json:"executable"`
	PID             int32     `json:"pid"`
	ParentPID       int32     `json:"parent_pid"`
	InitializedAt   time.Time `json:"initialized_at"`
	ModulePath      string    `json:"module_path"`
	ModuleVersion   string    `json:"module_version"`
	Revision        *string   `json:"revision"`
	RevisionTime    *string   `json:"revision_time"`
	Modified        *bool     `json:"modified"`
	ObservedAt      time.Time `json:"observed_at"`
	DurationNS      int64     `json:"duration_ns,string"`
}

func infoResult(result domain.InfoObservation) infoResponse {
	row := result.Info
	return infoResponse{GoVersion: row.GoVersion, Compiler: row.Compiler, OperatingSystem: row.OperatingSystem, Architecture: row.Architecture, Executable: row.Executable, PID: row.PID, ParentPID: row.ParentPID, InitializedAt: row.InitializedAt, ModulePath: row.ModulePath, ModuleVersion: row.ModuleVersion, Revision: row.Revision, RevisionTime: row.RevisionTime, Modified: row.Modified, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type resourcesResponse struct {
	PID                int32     `json:"pid"`
	StartTicks         uint64    `json:"start_ticks,string"`
	TicksPerSecond     uint64    `json:"ticks_per_second,string"`
	UserTicks          uint64    `json:"user_ticks,string"`
	SystemTicks        uint64    `json:"system_ticks,string"`
	VirtualBytes       uint64    `json:"virtual_bytes,string"`
	ResidentBytes      uint64    `json:"resident_bytes,string"`
	Threads            uint64    `json:"threads,string"`
	FileDescriptors    uint64    `json:"file_descriptors,string"`
	Goroutines         uint64    `json:"goroutines,string"`
	GoMaxProcs         uint64    `json:"go_max_procs,string"`
	RuntimeMappedBytes uint64    `json:"runtime_mapped_bytes,string"`
	HeapObjectsBytes   uint64    `json:"heap_objects_bytes,string"`
	HeapReleasedBytes  uint64    `json:"heap_released_bytes,string"`
	AllocatedBytes     uint64    `json:"allocated_bytes,string"`
	FreedBytes         uint64    `json:"freed_bytes,string"`
	GCCycles           uint64    `json:"gc_cycles,string"`
	ObservedAt         time.Time `json:"observed_at"`
	DurationNS         int64     `json:"duration_ns,string"`
}

func resourcesResult(result domain.ResourceObservation) resourcesResponse {
	row := result.Resources
	return resourcesResponse{PID: row.PID, StartTicks: row.StartTicks, TicksPerSecond: row.TicksPerSecond, UserTicks: row.UserTicks, SystemTicks: row.SystemTicks, VirtualBytes: row.VirtualBytes, ResidentBytes: row.ResidentBytes, Threads: row.Threads, FileDescriptors: row.FileDescriptors, Goroutines: row.Goroutines, GoMaxProcs: row.GoMaxProcs, RuntimeMappedBytes: row.RuntimeMappedBytes, HeapObjectsBytes: row.HeapObjectsBytes, HeapReleasedBytes: row.HeapReleasedBytes, AllocatedBytes: row.AllocatedBytes, FreedBytes: row.FreedBytes, GCCycles: row.GCCycles, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
