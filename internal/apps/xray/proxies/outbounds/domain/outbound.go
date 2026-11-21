// Package domain describes native outbound observations independently of SDKs.
package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidArgument  = errors.New("invalid outbound request")
	ErrNotFound         = errors.New("instance not found")
	ErrUnsupported      = errors.New("outbound API not configured or implemented")
	ErrNotRunning       = errors.New("instance is not running")
	ErrCapacity         = errors.New("outbound read capacity exceeded")
	ErrExecutionChanged = errors.New("execution changed during outbound observation")
)

const (
	MaxResponseBytes = 32 << 20
	MaxRecords       = 65536
)

// Record contains the native ListOutbounds projection as core.OutboundHandlerConfig
// protobuf: tag, sender settings and proxy settings. Xray does not retain source
// expire/comment fields here. This is not desired configuration or a default-route
// observation; native ordering is unspecified and untagged duplicates are retained.
type Record struct {
	Tag           string
	Configuration []byte
}
type Records struct {
	InstanceID string
	Generation uint64
	Outbounds  []Record
	ObservedAt time.Time
	Duration   time.Duration
}
