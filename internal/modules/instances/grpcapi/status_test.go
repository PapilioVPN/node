package grpcapi

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nodev1 "github.com/PapilioVPN/node/internal/entrypoints/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/modules/instances/domain"
)

func TestStatusResponsePreservesLifecycleAndFailurePresence(t *testing.T) {
	for observed, expected := range map[domain.State]nodev1.InstanceState{
		domain.Stopped:  nodev1.InstanceState_INSTANCE_STATE_STOPPED,
		domain.Running:  nodev1.InstanceState_INSTANCE_STATE_RUNNING,
		domain.Stopping: nodev1.InstanceState_INSTANCE_STATE_STOPPING,
		domain.Exited:   nodev1.InstanceState_INSTANCE_STATE_EXITED,
	} {
		response, err := statusResponse(domain.Status{Instance: domain.Instance{ID: "edge", Kind: domain.Xray}, State: observed})
		if err != nil || response.InstanceId != "edge" || response.Engine != "xray" || response.State != expected || response.Failure != nil {
			t.Fatalf("lifecycle mapping = %v, %v", response, err)
		}
	}
	if _, err := statusResponse(domain.Status{State: "invalid-private-state"}); status.Code(err) != codes.Internal || strings.Contains(err.Error(), "invalid-private-state") {
		t.Fatalf("unknown state mapping = %v", err)
	}
}

func TestStatusResponsePreservesExitCodeWithoutRawErrors(t *testing.T) {
	for _, code := range []int{0, 23, -1, 1 << 33} {
		response, err := statusResponse(domain.Status{State: domain.Exited, Failure: fmt.Errorf("private path: %w", codedFailure(code))})
		if err != nil || response.Failure.GetCode() != nodev1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED {
			t.Fatalf("execution failure mapping = %v, %v", response, err)
		}
		if code >= 0 && code <= 23 {
			if response.Failure.ExitCode == nil || int(response.Failure.GetExitCode()) != code {
				t.Fatalf("known exit code or its presence lost: %v", response)
			}
		} else if response.Failure.ExitCode != nil {
			t.Fatalf("unknown/out-of-range exit code was fabricated: %v", response)
		}
		if strings.Contains(response.String(), "private") {
			t.Fatal("raw execution error leaked")
		}
	}
	response, err := statusResponse(domain.Status{State: domain.Exited, Failure: errors.New("private runtime detail")})
	if err != nil || response.Failure == nil || response.Failure.ExitCode != nil {
		t.Fatalf("failure with unknown exit code lost: %v, %v", response, err)
	}
}

// Controlled error capability; real Xray exit codes are checked in native tests.
type codedFailure int

func (e codedFailure) Error() string { return "private execution detail" }
func (e codedFailure) ExitCode() int { return int(e) }
