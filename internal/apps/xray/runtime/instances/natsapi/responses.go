package natsapi

import (
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type statusResponse struct {
	ID                         string       `json:"instance_id"`
	Engine                     string       `json:"engine"`
	State                      domain.State `json:"state"`
	Generation                 string       `json:"generation"`
	StartedAt                  *time.Time   `json:"started_at"`
	ExitedAt                   *time.Time   `json:"exited_at"`
	ExitCode                   *int32       `json:"exit_code"`
	Forced                     bool         `json:"forced"`
	ExecutionFailed            bool         `json:"execution_failed"`
	ObservedAt                 time.Time    `json:"observed_at"`
	ConfigurationRevision      *string      `json:"configuration_revision,omitempty"`
	ConfigurationCleanupFailed bool         `json:"configuration_cleanup_failed,omitempty"`
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
	var revision *string
	if e.ConfigurationRevision != nil {
		value := strconv.FormatUint(*e.ConfigurationRevision, 10)
		revision = &value
	}
	return statusResponse{s.ID, s.Engine, e.State, strconv.FormatUint(e.Generation, 10), e.StartedAt, e.ExitedAt, e.ExitCode, e.Forced, e.Failure != nil, s.ObservedAt, revision, e.CleanupFailure != nil}
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
	return readinessResponse{statusResult(result.Status), string(result.APIState), string(result.TrafficState), result.CheckedAt}
}
