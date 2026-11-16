package domain

import "time"

// InboundUserDelta replaces selected memberships and removes selected identities;
// unlisted identities are never selected for removal. Native credential
// uniqueness/upsert behavior still applies. Identities must be distinct under
// the native protocol's matching rules, including across the two collections.
type InboundUserDelta struct {
	InboundTag   string
	Users        []InboundUser
	RemoveEmails []string
}

type UserMutationAction uint8

const (
	UserMutationRemove UserMutationAction = iota
	UserMutationAdd
)

// UserMutationStep contains no credentials. Repeated removals represent distinct
// existing native members with the same email; every receipt remains visible.
type UserMutationStep struct {
	Email          string
	Action         UserMutationAction
	State          UserMutationState
	AcknowledgedAt time.Time
	Failure        error
}

// UserDeltaReport retains the native membership observation and the complete
// planned effect sequence, including unattempted steps after a partial failure.
// ObservedAt does not establish a fenced snapshot or durable configuration.
type UserDeltaReport struct {
	ObservedAt time.Time
	Steps      []UserMutationStep
}
