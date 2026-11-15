// Package domain declares Xray-specific observations, separate from runtime control.
package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

const ReadBinaryInfo = "xray.binary.info.read"

var ErrInspectionDisabled = errors.New("Xray binary inspection is not enabled")
var ErrInspectionBusy = errors.New("Xray binary inspection is busy")
var ErrInfoOutputLimit = errors.New("Xray binary information exceeds the output limit")
var ErrInvalidInfoOutput = errors.New("unsupported Xray version output")

// BinaryInfo is observed from the current configured executable's version command.
// It does not establish an already running process's version or readiness.
type BinaryInfo struct {
	Version         string
	Build           string
	GoVersion       string
	OperatingSystem string
	Architecture    string
	ObservedAt      time.Time
}

type InstanceBinaryInfo struct {
	InstanceID instances.InstanceID
	Binary     BinaryInfo
}
