package natsapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/storage/devices/domain"
)

type discardResponse struct {
	Completed    uint64 `json:"completed,string"`
	Merged       uint64 `json:"merged,string"`
	Sectors      uint64 `json:"sectors,string"`
	Milliseconds uint64 `json:"milliseconds,string"`
}
type flushResponse struct {
	Completed    uint64 `json:"completed,string"`
	Milliseconds uint64 `json:"milliseconds,string"`
}
type deviceResponse struct {
	Major                uint32 `json:"major"`
	Minor                uint32 `json:"minor"`
	Name                 string `json:"name"`
	ReadsCompleted       uint64 `json:"reads_completed,string"`
	ReadsMerged          uint64 `json:"reads_merged,string"`
	ReadSectors          uint64 `json:"read_sectors,string"`
	ReadMilliseconds     uint64 `json:"read_milliseconds,string"`
	WritesCompleted      uint64 `json:"writes_completed,string"`
	WritesMerged         uint64 `json:"writes_merged,string"`
	WriteSectors         uint64 `json:"write_sectors,string"`
	WriteMilliseconds    uint64 `json:"write_milliseconds,string"`
	InProgress           uint64 `json:"in_progress,string"`
	ActiveMilliseconds   uint64 `json:"active_milliseconds,string"`
	WeightedMilliseconds uint64 `json:"weighted_milliseconds,string"`

	Discard *discardResponse `json:"discard"`
	Flush   *flushResponse   `json:"flush"`
}
type response struct {
	Devices         []deviceResponse `json:"devices"`
	NextAfterName   *string          `json:"next_after_name"`
	SectorSizeBytes uint32           `json:"sector_size_bytes"`
	ObservedAt      time.Time        `json:"observed_at"`
	DurationNS      int64            `json:"duration_ns,string"`
}

func listResult(result domain.Observation) response {
	rows := make([]deviceResponse, 0, len(result.Page.Devices))
	for _, row := range result.Page.Devices {
		value := deviceResponse{Major: row.Major, Minor: row.Minor, Name: row.Name, ReadsCompleted: row.ReadsCompleted, ReadsMerged: row.ReadsMerged, ReadSectors: row.ReadSectors, ReadMilliseconds: row.ReadMilliseconds, WritesCompleted: row.WritesCompleted, WritesMerged: row.WritesMerged, WriteSectors: row.WriteSectors, WriteMilliseconds: row.WriteMilliseconds, InProgress: row.InProgress, ActiveMilliseconds: row.ActiveMilliseconds, WeightedMilliseconds: row.WeightedMilliseconds}
		if row.Discard != nil {
			value.Discard = &discardResponse{Completed: row.Discard.Completed, Merged: row.Discard.Merged, Sectors: row.Discard.Sectors, Milliseconds: row.Discard.Milliseconds}
		}
		if row.Flush != nil {
			value.Flush = &flushResponse{Completed: row.Flush.Completed, Milliseconds: row.Flush.Milliseconds}
		}
		rows = append(rows, value)
	}
	return response{Devices: rows, NextAfterName: result.Page.NextAfterName, SectorSizeBytes: 512, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
}
