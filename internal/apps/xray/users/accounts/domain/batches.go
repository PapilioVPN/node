package domain

import "errors"

const (
	MaxBatchItems       = 1000
	MaxBatchParallelism = 4
	MaxBatchBytes       = 4 << 20
)

var ErrNotAttempted = errors.New("account batch stopped before this item")

type Addition struct {
	InboundTag    string
	Configuration []byte
}
type Removal struct {
	InboundTag string
	Email      string
}

// ItemResult preserves input position, actual native evidence and private cause.
// Attempted means the native adapter was invoked, not proof of network delivery.
type ItemResult struct {
	Index     int
	Attempted bool
	Operation Operation
	Err       error
}

// Batch is live and non-atomic; results retain input order, execution may overlap.
// Parallelism one preserves execution order for dependent or conflicting items.
// No durable acceptance, rollback or automatic replay is implied.
type Batch struct {
	Operation Operation
	Items     []ItemResult
}
