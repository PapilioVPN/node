package native

import (
	"maps"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/protobuf/proto"
)

const maxAcceptedBytes = 16 << 20

// acceptedMembership is immutable after publication. A nil account in a delta
// is an explicit removal. Full selections retain the complete named membership.
type acceptedMembership struct {
	inbounds map[string]acceptedInbound
}

type acceptedInbound struct {
	layout   accountLayout
	keySize  int
	full     bool
	accounts map[string]*User
	bytes    int
}

func (state *acceptedMembership) clone() *acceptedMembership {
	if state == nil {
		return &acceptedMembership{inbounds: make(map[string]acceptedInbound)}
	}
	return &acceptedMembership{inbounds: maps.Clone(state.inbounds)}
}

type editSession struct {
	editors []membership.Editor
	staged  *acceptedMembership
}

func (s *editSession) Inbounds() []membership.Editor { return s.editors }

func (s *editSession) CheckStaged() error {
	var records, size int
	for _, inbound := range s.staged.inbounds {
		records += len(inbound.accounts)
		size += inbound.bytes
	}
	if records > maxAccounts || size > maxAcceptedBytes {
		return membership.ErrTooLarge
	}
	return nil
}

func (e *editor) user(account membership.PreparedAccount) *User {
	return &User{Email: account.Email, Level: account.Level,
		Account: &TypedMessage{Type: e.entry.layout.accountType, Value: account.Value}}
}

func (e *editor) Stage(full bool, members []membership.PlannedMember, removals []string, additions []membership.PreparedAccount) error {
	if err := e.checkPeerRoutes(removals, additions); err != nil {
		return err
	}
	if !full && len(members) == 0 {
		return nil
	}
	current := e.staged.inbounds[e.scope.InboundTag]
	next := acceptedInbound{layout: e.entry.layout, keySize: e.entry.ss2022KeySize,
		accounts: make(map[string]*User, len(members)), full: full}
	if !full && current.accounts != nil && current.layout == next.layout && current.keySize == next.keySize {
		next.full = current.full
		next.accounts = maps.Clone(current.accounts)
		next.bytes = current.bytes
	}
	for _, member := range members {
		switch {
		case member.Keep:
			next.set(member.Name, e.entry.records[member.Name])
		case member.Replacement != nil:
			next.set(member.Name, e.user(*member.Replacement))
		default:
			next.set(member.Name, nil)
		}
	}
	e.staged.inbounds[e.scope.InboundTag] = next
	return nil
}

func (inbound *acceptedInbound) set(name string, user *User) {
	if previous, exists := inbound.accounts[name]; exists {
		inbound.bytes -= len(name) + proto.Size(previous)
	}
	if user == nil && inbound.full {
		delete(inbound.accounts, name)
		return
	}
	inbound.accounts[name] = user
	inbound.bytes += len(name) + proto.Size(user)
}
