package engines

import (
	"sync/atomic"
	"time"
)

// ManagementActivity records the newest authenticated management activity.
// Engines remain responsible for their own leases and expiry decisions.
type ManagementActivity struct {
	start time.Time
	last  atomic.Int64
}

func NewManagementActivity() *ManagementActivity {
	return &ManagementActivity{start: time.Now()}
}

func (activity *ManagementActivity) Mark() {
	mark := time.Since(activity.start).Nanoseconds()
	for last := activity.last.Load(); mark > last; last = activity.last.Load() {
		if activity.last.CompareAndSwap(last, mark) {
			return
		}
	}
}

// Extend never creates a lease or moves its deadline backwards.
func (activity *ManagementActivity) Extend(expiry time.Time, window time.Duration) time.Time {
	if activity != nil && !expiry.IsZero() && window > 0 {
		if last := activity.last.Load(); last != 0 {
			if next := activity.start.Add(time.Duration(last)).Add(window); next.After(expiry) {
				return next
			}
		}
	}
	return expiry
}
