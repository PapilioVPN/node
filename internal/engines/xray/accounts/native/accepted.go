package native

import (
	"maps"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/protobuf/proto"
)

const maxAcceptedBytes = 16 << 20

// The client serializes edits and recovery preparation with its mutation
// semaphore. A nil account in a delta is an explicit removal. Full selections
// retain the complete named membership.
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

type acceptedUpdate struct {
	name string
	user *User
}

type acceptedEdit struct {
	tag     string
	inbound acceptedInbound
	updates []acceptedUpdate
}

type editSession struct {
	editors                  []membership.Editor
	accepted                 *acceptedMembership
	staged                   []acceptedEdit
	recordsDelta, bytesDelta int
}

func (s *editSession) Inbounds() []membership.Editor { return s.editors }

func (s *editSession) CheckStaged() error {
	if len(s.staged) == 0 {
		return nil
	}
	records, size := s.recordsDelta, s.bytesDelta
	if s.accepted != nil {
		for _, inbound := range s.accepted.inbounds {
			records += len(inbound.accounts)
			size += inbound.bytes
		}
	}
	if records > maxAccounts || size > maxAcceptedBytes {
		return membership.ErrTooLarge
	}
	return nil
}

// publish runs under the mutation semaphore after every planned effect has been
// acknowledged. Failed plans can be discarded without changing accepted maps.
func (s *editSession) publish(c *Client) error {
	if len(s.staged) == 0 {
		if s.accepted != nil && c.recovery != nil && c.recovery.unsynced {
			return c.accept(s.accepted)
		}
		return nil
	}
	accepted := s.accepted
	if accepted == nil || c.recovery != nil {
		accepted = accepted.clone()
	}
	for _, edit := range s.staged {
		if c.recovery != nil && len(edit.updates) != 0 {
			edit.inbound.accounts = maps.Clone(edit.inbound.accounts)
		}
		for _, update := range edit.updates {
			edit.inbound.write(update.name, update.user)
		}
		accepted.inbounds[edit.tag] = edit.inbound
	}
	return c.accept(accepted)
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

	session := e.session
	tag := e.scope.InboundTag
	var current acceptedInbound
	if session.accepted != nil {
		current = session.accepted.inbounds[tag]
	}
	edit := acceptedEdit{tag: tag, inbound: acceptedInbound{layout: e.entry.layout, keySize: e.entry.ss2022KeySize, full: full}}
	next := &edit.inbound
	delta := !full && current.accounts != nil && current.layout == next.layout && current.keySize == next.keySize
	if delta {
		*next = current
	} else {
		next.accounts = make(map[string]*User, len(members))
	}

	records := len(next.accounts)
	for _, member := range members {
		var user *User
		switch {
		case member.Keep:
			user = e.entry.records[member.Name]
		case member.Replacement != nil:
			user = e.user(*member.Replacement)
		}
		if !delta {
			next.set(member.Name, user)
			continue
		}
		previous, exists := current.accounts[member.Name]
		if previous == user && (exists || current.full) {
			continue
		}
		count, size := current.sizeDelta(member.Name, user)
		records += count
		next.bytes += size
		edit.updates = append(edit.updates, acceptedUpdate{member.Name, user})
	}
	if delta && len(edit.updates) == 0 {
		return nil
	}
	if !delta {
		records = len(next.accounts)
	}
	session.recordsDelta += records - len(current.accounts)
	session.bytesDelta += next.bytes - current.bytes
	session.staged = append(session.staged, edit)
	return nil
}

func (inbound *acceptedInbound) sizeDelta(name string, user *User) (records, bytes int) {
	if previous, exists := inbound.accounts[name]; exists {
		records--
		bytes -= len(name) + proto.Size(previous)
	}
	if user != nil || !inbound.full {
		records++
		bytes += len(name) + proto.Size(user)
	}
	return records, bytes
}

func (inbound *acceptedInbound) set(name string, user *User) {
	_, size := inbound.sizeDelta(name, user)
	inbound.bytes += size
	inbound.write(name, user)
}

func (inbound *acceptedInbound) write(name string, user *User) {
	if user == nil && inbound.full {
		delete(inbound.accounts, name)
		return
	}
	inbound.accounts[name] = user
}
