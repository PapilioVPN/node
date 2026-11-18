package httpapi

import (
	"github.com/PapilioVPN/node/internal/apps/system/host/processes/domain"
	"time"
)

type processResponse struct {
	PID           int32  `json:"pid"`
	ParentPID     int32  `json:"parent_pid"`
	Name          string `json:"name"`
	State         string `json:"state"`
	Threads       uint32 `json:"threads"`
	Priority      int32  `json:"priority"`
	Nice          int32  `json:"nice"`
	StartTicks    uint64 `json:"start_ticks,string"`
	UserTicks     uint64 `json:"user_ticks,string"`
	SystemTicks   uint64 `json:"system_ticks,string"`
	VirtualBytes  uint64 `json:"virtual_bytes,string"`
	ResidentBytes uint64 `json:"resident_bytes,string"`
}

func processResult(row domain.Process) processResponse {
	return processResponse{PID: row.PID, ParentPID: row.ParentPID, Name: row.Name, State: row.State, Threads: row.Threads, Priority: row.Priority, Nice: row.Nice, StartTicks: row.StartTicks, UserTicks: row.UserTicks, SystemTicks: row.SystemTicks, VirtualBytes: row.VirtualBytes, ResidentBytes: row.ResidentBytes}
}

type entryResponse struct {
	PID     int32            `json:"pid"`
	Process *processResponse `json:"process"`
	Issue   string           `json:"issue"`
}
type listResponse struct {
	Entries        []entryResponse `json:"entries"`
	NextAfterPID   *int32          `json:"next_after_pid"`
	TicksPerSecond uint64          `json:"ticks_per_second,string"`
	ObservedAt     time.Time       `json:"observed_at"`
	DurationNS     int64           `json:"duration_ns,string"`
}

func listResult(result domain.ListObservation) listResponse {
	rows := make([]entryResponse, 0, len(result.Page.Entries))
	for _, entry := range result.Page.Entries {
		row := entryResponse{PID: entry.PID, Issue: entry.Issue}
		if entry.Process != nil {
			value := processResult(*entry.Process)
			row.Process = &value
		}
		rows = append(rows, row)
	}
	return listResponse{Entries: rows, NextAfterPID: result.Page.NextAfterPID, TicksPerSecond: result.TicksPerSecond, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type readResponse struct {
	Process        processResponse `json:"process"`
	TicksPerSecond uint64          `json:"ticks_per_second,string"`
	ObservedAt     time.Time       `json:"observed_at"`
	DurationNS     int64           `json:"duration_ns,string"`
}

func readResult(result domain.Observation) readResponse {
	return readResponse{Process: processResult(result.Process), TicksPerSecond: result.TicksPerSecond, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}

type countResponse struct {
	Count      uint64    `json:"count,string"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
}

func countResult(result domain.CountObservation) countResponse {
	return countResponse{Count: result.Count, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
