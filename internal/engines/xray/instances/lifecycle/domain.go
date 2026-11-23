package lifecycle

import "errors"

var (
	ErrNotFound    = errors.New("xray instance not found")
	ErrBusy        = errors.New("xray instance is changing state")
	ErrUnavailable = errors.New("xray instance manager is shutting down")
	ErrStartFailed = errors.New("xray failed to start")
	ErrStopFailed  = errors.New("xray failed to stop")
)

type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Ready    State = "ready"
	Stopping State = "stopping"
	Failed   State = "failed"
)

type Status struct {
	ID    string
	State State
}
