package httpapi

import (
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
)

type statusResponse struct {
	ID              string       `json:"instance_id"`
	Engine          string       `json:"engine"`
	State           domain.State `json:"state"`
	Generation      string       `json:"generation"`
	StartedAt       *time.Time   `json:"started_at"`
	ExitedAt        *time.Time   `json:"exited_at"`
	ExitCode        *int32       `json:"exit_code"`
	Forced          bool         `json:"forced"`
	ExecutionFailed bool         `json:"execution_failed"`
	ObservedAt      time.Time    `json:"observed_at"`
}
type controlResponse struct {
	Status *statusResponse `json:"status"`
	Effect domain.Effect   `json:"effect"`
}
type errorResponse struct {
	Error   string           `json:"error"`
	Control *controlResponse `json:"control,omitempty"`
}
type readinessResponse struct {
	Status       statusResponse `json:"status"`
	APIState     string         `json:"api_state"`
	TrafficState string         `json:"traffic_state"`
	CheckedAt    time.Time      `json:"checked_at"`
}

func statusResult(s domain.Status) statusResponse {
	e := s.Execution
	return statusResponse{s.ID, s.Engine, e.State, strconv.FormatUint(e.Generation, 10), e.StartedAt, e.ExitedAt, e.ExitCode, e.Forced, e.Failure != nil, s.ObservedAt}
}
func controlResult(result domain.Control) controlResponse {
	response := controlResponse{Effect: result.Effect}
	if result.Status != nil {
		status := statusResult(*result.Status)
		response.Status = &status
	}
	return response
}
func readinessResult(result domain.Readiness) readinessResponse {
	state := "not_configured"
	if result.APIConfigured {
		state = "not_checked"
		if result.Status.Execution.State == domain.Running {
			state = "unavailable"
			if result.APIReady {
				state = "ready"
			}
		}
	}
	return readinessResponse{statusResult(result.Status), state, "not_checked", result.CheckedAt}
}
