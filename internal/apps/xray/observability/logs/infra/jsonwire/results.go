package jsonwire

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

type Record struct {
	StartOffset uint64 `json:"start_offset,string"`
	NextOffset  uint64 `json:"next_offset,string"`
	Level       string `json:"level"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	Redacted    bool   `json:"redacted"`
	Truncated   bool   `json:"truncated"`
	Partial     bool   `json:"partial"`
}
type Gap struct {
	Reason              string `json:"reason"`
	FromOffset          uint64 `json:"from_offset,string"`
	ToOffset            uint64 `json:"to_offset,string"`
	ExtentKnown         bool   `json:"extent_known"`
	PreviousExecutionID string `json:"previous_execution_id"`
}
type Page struct {
	InstanceID        string    `json:"instance_id"`
	Source            string    `json:"source"`
	ExecutionID       string    `json:"execution_id"`
	Generation        uint64    `json:"generation,string"`
	ObservedAt        time.Time `json:"observed_at"`
	Records           []Record  `json:"records"`
	Gaps              []Gap     `json:"gaps"`
	NextCursor        string    `json:"next_cursor"`
	CapacityReached   bool      `json:"capacity_reached"`
	PositionUncertain bool      `json:"position_uncertain"`
	Reattached        bool      `json:"reattached"`
	Exited            bool      `json:"exited"`
}

func Encode(p domain.Page) ([]byte, error) {
	r := Page{InstanceID: p.InstanceID, Source: p.Source, ExecutionID: p.ExecutionID, Generation: p.Generation, ObservedAt: p.ObservedAt, Records: make([]Record, 0, len(p.Records)), Gaps: make([]Gap, 0, len(p.Gaps)), NextCursor: p.NextCursor, CapacityReached: p.CapacityReached, PositionUncertain: p.PositionUncertain, Reattached: p.Reattached, Exited: p.Exited}
	for _, v := range p.Records {
		r.Records = append(r.Records, Record{v.StartOffset, v.NextOffset, v.Level, v.Kind, v.Message, v.Redacted, v.Truncated, v.Partial})
	}
	for _, v := range p.Gaps {
		r.Gaps = append(r.Gaps, Gap{v.Reason, v.FromOffset, v.ToOffset, v.ExtentKnown, v.PreviousExecutionID})
	}
	return json.Marshal(r)
}
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, domain.ErrInvalidArgument):
		return "invalid_argument"
	case errors.Is(err, domain.ErrNotFound):
		return "not_found"
	case errors.Is(err, domain.ErrUnsupported):
		return "output_unavailable"
	case errors.Is(err, domain.ErrCapacity):
		return "resource_exhausted"
	case errors.Is(err, domain.ErrExecutionChanged):
		return "execution_changed"
	case errors.Is(err, domain.ErrClosed):
		return "closed"
	default:
		return "unavailable"
	}
}
func Failure(err error) []byte {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{ErrorCode(err)})
	return data
}
