package lifecycle

import (
	"context"
	"errors"
)

const MaxSourceBytes = 1 << 20

var (
	ErrNotFound          = errors.New("openvpn instance not found")
	ErrBusy              = errors.New("openvpn instance is busy")
	ErrTooLarge          = errors.New("openvpn configuration exceeds 1 MiB")
	ErrSourceUnavailable = errors.New("openvpn configuration is unavailable")
	ErrRejected          = errors.New("openvpn configuration was rejected")
	ErrCheckFailed       = errors.New("openvpn configuration check failed")
	ErrStartFailed       = errors.New("openvpn failed to reach readiness")
	ErrStopFailed        = errors.New("openvpn stop could not be confirmed")
)

type State string

const (
	Stopped  State = "stopped"
	Starting State = "starting"
	Ready    State = "ready"
	Stopping State = "stopping"
	Failed   State = "failed"
)

type StartAction string

const (
	Started   StartAction = "started"
	Heartbeat StartAction = "heartbeat"
	Restarted StartAction = "restarted"
	Recovered StartAction = "recovered"
)

type Status struct {
	ID                  string
	State               State
	NodeVersion         string
	OpenVPNVersion      string
	AppliedConfigSHA256 string
	StartAction         StartAction
}

type Issue struct {
	Code string
	Line uint32
}

type Validation struct {
	ID    string
	Issue Issue
}

type Execution interface {
	Done() <-chan struct{}
	Version() string
	Stop(context.Context) error
}

// Driver validates source syntax and launches an execution through confirmed readiness.
type Driver interface {
	Check(context.Context, []byte) (Issue, error)
	Start(context.Context, []byte) (Execution, error)
}
