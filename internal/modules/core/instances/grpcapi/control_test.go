package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
)

func controlMethods(handler grpcapi.Handler) map[string]func(context.Context, string) (*instancesv1.InstanceStatus, error) {
	return map[string]func(context.Context, string) (*instancesv1.InstanceStatus, error){
		"start": func(ctx context.Context, id string) (*instancesv1.InstanceStatus, error) {
			return handler.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: id})
		},
		"stop": func(ctx context.Context, id string) (*instancesv1.InstanceStatus, error) {
			return handler.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: id})
		},
	}
}

func TestControlDeniedBeforeLookupByDefault(t *testing.T) {
	for name, operation := range controlMethods(grpcapi.Handler{}) {
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				ctx  context.Context
				code codes.Code
			}{{context.Background(), codes.Unauthenticated}, {verifiedContext(t), codes.PermissionDenied}} {
				if _, err := operation(test.ctx, "unknown"); status.Code(err) != test.code {
					t.Fatalf("default control authorization = %v; want %v", err, test.code)
				}
			}
		})
	}
}

func TestControlAuthenticationAndIndependentGrants(t *testing.T) {
	ctx := verifiedContext(t)
	for name, operation := range controlMethods(grpcapi.Handler{AllowStatusRead: true}) {
		if _, err := operation(ctx, ""); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: read grant authorized control: %v", name, err)
		}
	}
	handler := grpcapi.Handler{AllowControl: true}
	if _, err := handler.GetInstanceStatus(ctx, &instancesv1.GetInstanceStatusRequest{InstanceId: "edge"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("control grant authorized status reads: %v", err)
	}
	for name, operation := range controlMethods(handler) {
		if _, err := operation(context.Background(), ""); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s: control grant bypassed mTLS: %v", name, err)
		}
		for _, id := range []string{"", "  "} {
			if _, err := operation(ctx, id); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("%s: blank identity reached controller: %v", name, err)
			}
		}
	}
	if _, err := handler.StartInstance(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.StopInstance(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}

// controllerMock records the application operation without executing an engine.
type controllerMock struct {
	start func(context.Context, domain.InstanceID) (domain.Status, error)
	stop  func(context.Context, domain.InstanceID) (domain.Status, error)
}

func (m controllerMock) StartInstance(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	return m.start(ctx, id)
}
func (m controllerMock) StopInstance(ctx context.Context, id domain.InstanceID) (domain.Status, error) {
	return m.stop(ctx, id)
}

func TestControlPreservesTargetContextAndOutcome(t *testing.T) {
	ctx := verifiedContext(t)
	var starts, stops int
	controller := controllerMock{
		start: func(actual context.Context, id domain.InstanceID) (domain.Status, error) {
			starts++
			if actual != ctx || id != " edge-a " {
				t.Fatal("start target/context changed")
			}
			return domain.Status{Instance: domain.Instance{ID: id, Kind: domain.Xray}, State: domain.Running}, nil
		},
		stop: func(actual context.Context, id domain.InstanceID) (domain.Status, error) {
			stops++
			if actual != ctx || id != " edge-a " {
				t.Fatal("stop target/context changed")
			}
			return domain.Status{Instance: domain.Instance{ID: id, Kind: domain.Xray}, State: domain.Stopped}, nil
		},
	}
	handler := grpcapi.Handler{Control: controller, AllowControl: true}
	started, err := handler.StartInstance(ctx, &instancesv1.StartInstanceRequest{InstanceId: " edge-a "})
	if err != nil || started.GetInstanceId() != " edge-a " || started.GetEngine() != "xray" || started.GetState() != instancesv1.InstanceState_INSTANCE_STATE_RUNNING || started.Failure != nil {
		t.Fatalf("start: %v, %v", started, err)
	}
	stopped, err := handler.StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: " edge-a "})
	if err != nil || stopped.GetState() != instancesv1.InstanceState_INSTANCE_STATE_STOPPED || stopped.GetInstanceId() != " edge-a " || starts != 1 || stops != 1 {
		t.Fatalf("stop: %v, %v; calls %d/%d", stopped, err, starts, stops)
	}
}

func TestControlErrorsRetainSanitizedPartialObservation(t *testing.T) {
	ctx := verifiedContext(t)
	observed := domain.Status{Instance: domain.Instance{ID: "edge-a", Kind: domain.Xray}, State: domain.Exited, Failure: controlExitCode(23)}
	for _, test := range []struct {
		name    string
		cause   error
		value   domain.Status
		code    codes.Code
		details bool
	}{
		{"missing", fmt.Errorf("private path: %w", domain.ErrNotFound), domain.Status{}, codes.NotFound, false},
		{"cancellation precedes absence", errors.Join(domain.ErrNotFound, context.Canceled), domain.Status{}, codes.Canceled, false},
		{"canceled before selection", fmt.Errorf("private path: %w", context.Canceled), domain.Status{}, codes.Canceled, false},
		{"deadline after admission", fmt.Errorf("private path: %w", context.DeadlineExceeded), observed, codes.DeadlineExceeded, true},
		{"canceled after admission", fmt.Errorf("private path: %w", context.Canceled), observed, codes.Canceled, true},
		{"execution failed", errors.New("private native path"), observed, codes.Internal, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation := func(context.Context, domain.InstanceID) (domain.Status, error) { return test.value, test.cause }
			handler := grpcapi.Handler{Control: controllerMock{start: operation, stop: operation}, AllowControl: true}
			for name, call := range controlMethods(handler) {
				response, err := call(ctx, "edge-a")
				failure := status.Convert(err)
				if response != nil || failure.Code() != test.code || strings.Contains(err.Error(), "private") {
					t.Fatalf("%s: failure mapping: %v, %v", name, response, err)
				}
				details := failure.Details()
				if !test.details {
					if len(details) != 0 {
						t.Fatalf("%s: invented outcome: %v", name, details)
					}
					continue
				}
				if len(details) != 1 {
					t.Fatalf("%s: lost partial outcome: %v", name, details)
				}
				actual, ok := details[0].(*instancesv1.InstanceStatus)
				if !ok || actual.InstanceId != "edge-a" || actual.Engine != "xray" || actual.State != instancesv1.InstanceState_INSTANCE_STATE_EXITED || actual.Failure == nil || actual.Failure.Code != instancesv1.InstanceFailureCode_INSTANCE_FAILURE_CODE_EXECUTION_FAILED || actual.Failure.ExitCode == nil || *actual.Failure.ExitCode != 23 || strings.Contains(actual.String(), "private") {
					t.Fatalf("%s: execution cause leaked or partial observation lost: %v", name, details)
				}
			}
		})
	}
}

type controlExitCode int

func (c controlExitCode) Error() string { return "private engine execution error" }
func (c controlExitCode) ExitCode() int { return int(c) }
