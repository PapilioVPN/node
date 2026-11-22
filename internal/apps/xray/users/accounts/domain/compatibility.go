package domain

import "errors"

var (
	ErrIncompatible     = errors.New("account type is incompatible with inbound")
	ErrInboundMissing   = errors.New("inbound not found")
	ErrInboundAmbiguous = errors.New("inbound tag is ambiguous")
)

// Inbound is structural evidence supplied by the Inbounds owner. It is not a
// membership observation, a credential check or a fence against external writers.
type Inbound struct {
	Tag, Protocol, AccountType, Reason, EmailMatching string
	Generation                                        uint64
	LiveAccounts                                      bool
	Err                                               error
}
type Guidance struct{ Reason, NextAction, Protocol, ExpectedAccountType, ActualAccountType string }
type Compatibility struct {
	Observation
	Inbound              Inbound
	RequestedAccountType *string
	Compatible           *bool
	Guidance             Guidance
}
