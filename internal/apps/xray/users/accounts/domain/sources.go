package domain

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
	Err           error
}
