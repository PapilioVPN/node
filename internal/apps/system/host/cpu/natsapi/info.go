package natsapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

func (handler Handler) ReadInfo(ctx context.Context, data []byte, headers nats.Header) []byte {
	if len(data) > 1024 {
		return failure("resource_exhausted")
	}
	var input map[string]json.RawMessage
	if err := json.Unmarshal(data, &input); err != nil || input == nil || len(input) != 0 {
		return failure("invalid_argument")
	}
	ctx, cancel, code := requestContext(ctx, headers, handler.Timeout)
	if code != "" {
		return failure(code)
	}
	defer cancel()
	result, err := handler.Service.ReadInfo(ctx)
	if err != nil {
		return readError(err)
	}
	rows := make([]processorResponse, 0, len(result.Info.Processors))
	for _, row := range result.Info.Processors {
		rows = append(rows, processorResponse{ID: row.ID, VendorID: row.VendorID, Family: row.Family, Model: row.Model, ModelName: row.ModelName, Stepping: row.Stepping, Microcode: row.Microcode, PackageID: row.PackageID, CoreID: row.CoreID, ReportedCores: row.ReportedCores, ReportedSiblings: row.ReportedSiblings, ReportedMHz: row.ReportedMHz, CacheBytes: row.CacheBytes, ReportedFeatures: row.ReportedFeatures, ReportedBugs: row.ReportedBugs})
	}
	encoded, err := json.Marshal(infoResponse{LogicalCount: result.Info.LogicalCount, PackageCount: result.Info.PackageCount, PhysicalCoreCount: result.Info.PhysicalCoreCount, Processors: rows, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)})
	if err != nil {
		return failure("unavailable")
	}
	return encoded
}

type processorResponse struct {
	ID               uint32   `json:"id"`
	VendorID         *string  `json:"vendor_id"`
	Family           *string  `json:"family"`
	Model            *string  `json:"model"`
	ModelName        *string  `json:"model_name"`
	Stepping         *string  `json:"stepping"`
	Microcode        *string  `json:"microcode"`
	PackageID        *int32   `json:"package_id"`
	CoreID           *int32   `json:"core_id"`
	ReportedCores    *uint32  `json:"reported_cores"`
	ReportedSiblings *uint32  `json:"reported_siblings"`
	ReportedMHz      *float64 `json:"reported_mhz"`
	CacheBytes       *uint64  `json:"cache_bytes,string"`
	ReportedFeatures []string `json:"reported_features"`
	ReportedBugs     []string `json:"reported_bugs"`
}

type infoResponse struct {
	LogicalCount      uint32              `json:"logical_count"`
	PackageCount      *uint32             `json:"package_count"`
	PhysicalCoreCount *uint32             `json:"physical_core_count"`
	Processors        []processorResponse `json:"processors"`
	ObservedAt        time.Time           `json:"observed_at"`
	DurationNS        int64               `json:"duration_ns,string"`
}
