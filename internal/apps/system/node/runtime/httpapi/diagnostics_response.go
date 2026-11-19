package httpapi

import (
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type listenerResponse struct {
	Ready bool `json:"ready"`
}
type brokerResponse struct {
	State          string  `json:"state"`
	ProbeSucceeded bool    `json:"probe_succeeded"`
	RoundTripNS    *int64  `json:"round_trip_ns,string"`
	ErrorCode      *string `json:"error_code"`
	InMessages     uint64  `json:"in_messages,string"`
	OutMessages    uint64  `json:"out_messages,string"`
	InBytes        uint64  `json:"in_bytes,string"`
	OutBytes       uint64  `json:"out_bytes,string"`
	Reconnects     uint64  `json:"reconnects,string"`
}
type diagnosticsResponse struct {
	Healthy    bool              `json:"healthy"`
	HTTP       *listenerResponse `json:"http"`
	GRPC       *listenerResponse `json:"grpc"`
	NATS       *brokerResponse   `json:"nats"`
	ObservedAt time.Time         `json:"observed_at"`
	DurationNS int64             `json:"duration_ns,string"`
}

func diagnosticsResult(result domain.DiagnosticObservation) diagnosticsResponse {
	response := diagnosticsResponse{Healthy: result.Healthy, ObservedAt: result.ObservedAt, DurationNS: int64(result.Duration)}
	if result.Diagnostics.HTTP != nil {
		response.HTTP = &listenerResponse{Ready: result.Diagnostics.HTTP.Ready}
	}
	if result.Diagnostics.GRPC != nil {
		response.GRPC = &listenerResponse{Ready: result.Diagnostics.GRPC.Ready}
	}
	if row := result.Diagnostics.NATS; row != nil {
		broker := &brokerResponse{State: row.State, ProbeSucceeded: row.ProbeSucceeded, InMessages: row.InMessages, OutMessages: row.OutMessages, InBytes: row.InBytes, OutBytes: row.OutBytes, Reconnects: row.Reconnects}
		if row.ProbeSucceeded {
			duration := int64(row.RoundTrip)
			broker.RoundTripNS = &duration
		}
		if row.ErrorCode != "" {
			code := row.ErrorCode
			broker.ErrorCode = &code
		}
		response.NATS = broker
	}
	return response
}
