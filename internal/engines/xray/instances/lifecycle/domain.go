package lifecycle

import "errors"

var (
	ErrNotFound    = errors.New("xray instance not found")
	ErrBusy        = errors.New("xray instance is changing state")
	ErrNotReady    = errors.New("xray instance is not ready")
	ErrUnavailable = errors.New("xray instance manager is shutting down")
	ErrStartFailed = errors.New("xray failed to start")
	ErrStopFailed  = errors.New("xray failed to stop")
)

type StartFailureReason string

const (
	LaunchFailed      StartFailureReason = "launch_failed"
	ExitedBeforeReady StartFailureReason = "exited_before_ready"
	ReadinessTimeout  StartFailureReason = "readiness_timeout"
	ReadinessFailed   StartFailureReason = "readiness_failed"
)

type StartError struct {
	Reason   StartFailureReason
	ExitCode *int32
	cause    error
}

func (err *StartError) Error() string { return ErrStartFailed.Error() }

func (err *StartError) Is(target error) bool { return target == ErrStartFailed }

func (err *StartError) Unwrap() error { return err.cause }

type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Ready    State = "ready"
	Stopping State = "stopping"
	Failed   State = "failed"
)

type Status struct {
	ID          string
	State       State
	XrayVersion string
	NodeVersion string
}
