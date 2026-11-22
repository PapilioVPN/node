package domain

import "context"

// Addition owns a decoded native handler for one admitted source operation.
// Preparation has no native effect; the service checks execution before Add.
type Addition interface {
	Add(context.Context) (NativeResult, error)
}
