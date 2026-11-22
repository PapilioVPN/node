package domain

import "context"

// SourceSelection selects an exact source inbound and email, including an
// explicitly anonymous account. The live target has the same inbound tag and
// must have the same native account family; no other source settings are applied.
type SourceSelection struct {
	InboundTag string
	Email      string
}

// SourceAccount carries private preparation evidence in input order. A failed
// selection has no configuration and does not invoke the native account adapter.
type SourceAccount struct {
	Configuration []byte
	Prepared      PreparedAccount
	Err           error
}

// PreparedAccount owns one decoded native user until the admitted source
// operation completes. The service checks compatibility before submitting it.
type PreparedAccount interface {
	AccountType() string
	Add(context.Context, string) (NativeResult, error)
}
