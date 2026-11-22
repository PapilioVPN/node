package domain

import "context"

// Addition owns a prepared native handler until its synchronous submission.
// Preparation performs no engine mutation; the service admits the mutation and
// checks the execution generation before calling Add. Native types stay private
// to the adapter and the prepared handler is never persisted or exported.
type Addition interface {
	Add(context.Context) (NativeResult, error)
}
