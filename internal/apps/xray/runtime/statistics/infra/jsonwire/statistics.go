// Package jsonwire owns the runtime-statistics JSON contract shared by HTTP/NATS.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/statistics/domain"
)

func DecodeRequest(data []byte) (string, error) {
	if len(data) > 2048 {
		return "", domain.ErrInvalidArgument
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return "", domain.ErrInvalidArgument
	}
	if key, err := decoder.Token(); err != nil || key != "instance_id" {
		return "", domain.ErrInvalidArgument
	}
	var id string
	if err := decoder.Decode(&id); err != nil {
		return "", domain.ErrInvalidArgument
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return "", domain.ErrInvalidArgument
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return "", domain.ErrInvalidArgument
	}
	return id, nil
}
func Encode(result domain.Observation) ([]byte, error) {
	s := result.Statistics
	return json.Marshal(struct {
		InstanceID              string    `json:"instance_id"`
		Generation              uint64    `json:"generation,string"`
		Goroutines              uint32    `json:"goroutines"`
		GCCycles                uint32    `json:"gc_cycles"`
		HeapAllocatedBytes      uint64    `json:"heap_allocated_bytes,string"`
		HeapTotalAllocatedBytes uint64    `json:"heap_total_allocated_bytes,string"`
		RuntimeSysBytes         uint64    `json:"runtime_sys_bytes,string"`
		Allocations             uint64    `json:"allocations,string"`
		Frees                   uint64    `json:"frees,string"`
		LiveObjects             uint64    `json:"live_objects,string"`
		GCPauseTotalNS          uint64    `json:"gc_pause_total_ns,string"`
		APIUptimeSeconds        uint32    `json:"api_uptime_seconds"`
		ObservedAt              time.Time `json:"observed_at"`
		DurationNS              int64     `json:"duration_ns,string"`
	}{result.InstanceID, result.Generation, s.Goroutines, s.GCCycles, s.HeapAllocatedBytes, s.HeapTotalAllocatedBytes, s.RuntimeSysBytes, s.Allocations, s.Frees, s.LiveObjects, s.GCPauseTotalNS, s.APIUptimeSeconds, result.ObservedAt, int64(result.Duration)})
}
