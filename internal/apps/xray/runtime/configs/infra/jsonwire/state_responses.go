package jsonwire

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type metadataResponse struct {
	Revision    string     `json:"revision"`
	SHA256      string     `json:"sha256"`
	UpdatedAt   *time.Time `json:"updated_at"`
	SourceCount int        `json:"source_count"`
	Durable     bool       `json:"durable"`
}

func metadataResult(m domain.DesiredMetadata) metadataResponse {
	result := metadataResponse{Revision: strconv.FormatUint(m.Revision, 10), SHA256: m.SHA256, SourceCount: m.SourceCount, Durable: m.Durable}
	if !m.UpdatedAt.IsZero() {
		result.UpdatedAt = &m.UpdatedAt
	}
	return result
}

type sourceResponse struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}

func EncodeMetadata(id string, m domain.DesiredMetadata) ([]byte, error) {
	return json.Marshal(struct {
		InstanceID string           `json:"instance_id"`
		Metadata   metadataResponse `json:"metadata"`
	}{id, metadataResult(m)})
}
func EncodeDesired(id string, desired domain.Desired) ([]byte, error) {
	sources := make([]sourceResponse, len(desired.Sources))
	for i, s := range desired.Sources {
		sources[i] = sourceResponse{s.Name, s.Content}
	}
	return json.Marshal(struct {
		InstanceID string           `json:"instance_id"`
		Metadata   metadataResponse `json:"metadata"`
		Sources    []sourceResponse `json:"sources"`
	}{id, metadataResult(desired.Metadata), sources})
}

type persistenceResponse struct {
	Metadata           metadataResponse `json:"metadata"`
	Published          bool             `json:"published"`
	PublicationUnknown bool             `json:"publication_unknown"`
	Durable            bool             `json:"durable"`
}
type executionResponse struct {
	Effect instances.Effect `json:"effect"`
	Status *executionStatus `json:"status"`
}
type executionStatus struct {
	InstanceID                 string          `json:"instance_id"`
	Engine                     string          `json:"engine"`
	State                      instances.State `json:"state"`
	Generation                 string          `json:"generation"`
	ConfigurationRevision      *string         `json:"configuration_revision,omitempty"`
	StartedAt                  *time.Time      `json:"started_at"`
	ExitedAt                   *time.Time      `json:"exited_at"`
	ExitCode                   *int32          `json:"exit_code"`
	Forced                     bool            `json:"forced"`
	ExecutionFailed            bool            `json:"execution_failed"`
	ConfigurationCleanupFailed bool            `json:"configuration_cleanup_failed,omitempty"`
	ObservedAt                 time.Time       `json:"observed_at"`
}

// EncodeChange includes persistence and process evidence even on a failed change.
// Only stable error codes, never native causes or credential-bearing sources, cross this boundary.
func EncodeChange(id string, change domain.Change, code string) ([]byte, error) {
	var execution *executionResponse
	if c := change.Execution; c != nil {
		execution = &executionResponse{Effect: c.Effect}
		if s := c.Status; s != nil {
			e := s.Execution
			execution.Status = &executionStatus{InstanceID: s.ID, Engine: s.Engine, State: e.State, Generation: strconv.FormatUint(e.Generation, 10), StartedAt: e.StartedAt, ExitedAt: e.ExitedAt, ExitCode: e.ExitCode, Forced: e.Forced, ExecutionFailed: e.Failure != nil, ConfigurationCleanupFailed: e.CleanupFailure != nil, ObservedAt: s.ObservedAt}
			if e.ConfigurationRevision != nil {
				revision := strconv.FormatUint(*e.ConfigurationRevision, 10)
				execution.Status.ConfigurationRevision = &revision
			}
		}
	}
	p := change.Persistence
	return json.Marshal(struct {
		InstanceID  string              `json:"instance_id"`
		Persistence persistenceResponse `json:"persistence"`
		Execution   *executionResponse  `json:"execution"`
		Error       string              `json:"error,omitempty"`
	}{id, persistenceResponse{metadataResult(p.Metadata), p.Published, p.PublicationUnknown, p.Durable}, execution, code})
}
