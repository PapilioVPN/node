package domain

import (
	"errors"
	"time"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

const ReadRuntimeInfo = "xray.runtime.info.read"

var ErrAPIDisabled = engineapi.ErrDisabled
var ErrAPIBusy = engineapi.ErrBusy
var ErrAPIResourceLimit = engineapi.ErrResourceLimit
var ErrRuntimeInfoUnsupported = errors.New("Xray runtime information is not supported")

// RuntimeInfo preserves the native GetSysStats counters without signed conversion.
// SysBytes is Go MemStats.Sys, not RSS. Uptime starts at StatsService construction.
type RuntimeInfo struct {
	Goroutines          uint32
	GCCount             uint32
	AllocatedBytes      uint64
	TotalAllocatedBytes uint64
	SysBytes            uint64
	Mallocs             uint64
	Frees               uint64
	LiveObjects         uint64
	TotalGCPauseNS      uint64
	UptimeSeconds       uint32
	ObservedAt          time.Time
}

type InstanceRuntimeInfo struct {
	InstanceID instances.InstanceID
	Runtime    RuntimeInfo
}
