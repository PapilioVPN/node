package domain

import "time"

// Activity contains system-wide scheduler and interrupt counts from /proc/stat.
// The first four counters are cumulative since boot. Runnable and blocked tasks
// are instantaneous gauges; they are not counts of distinct processes or CPUs.
type Activity struct {
	ContextSwitches uint64
	Interrupts      uint64
	SoftInterrupts  uint64
	CreatedTasks    uint64
	RunnableTasks   uint64
	IOBlockedTasks  uint64
}

type ActivityObservation struct {
	Activity   Activity
	ObservedAt time.Time
	Duration   time.Duration
}
