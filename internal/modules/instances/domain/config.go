package domain

import "errors"

var ErrValidationDisabled = errors.New("instance configuration validation is not enabled")
var ErrValidationBusy = errors.New("instance configuration validation is busy")
var ErrConfigTooLarge = errors.New("candidate configuration exceeds the byte limit")

type ConfigFormat string

const (
	JSON ConfigFormat = "json"
	YAML ConfigFormat = "yaml"
	TOML ConfigFormat = "toml"
)

// ConfigCandidate borrows native bytes for a synchronous validation call.
// The caller must not mutate Content until the call returns.
type ConfigCandidate struct {
	Format  ConfigFormat
	Content []byte
}

// ConfigValidation reports native configuration building, not runtime readiness.
// Failure retains a completed native rejection privately; execution failures
// and cancellation belong to the operation's error result instead.
type ConfigValidation struct {
	Valid   bool
	Failure error
}
