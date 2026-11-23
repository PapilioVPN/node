package membership

const MaxSyncInbounds = 32

// InboundSnapshot describes complete named membership to initialize on startup.
type InboundSnapshot = struct {
	InboundTag string
	Accounts   []AccountInput
}

type Assignment struct {
	InboundTag string     `json:"inbound_tag"`
	Credential Credential `json:"credential"`
}

type selection struct {
	instanceID string
	tags       []string
	explicit   bool
}

func selectInbounds(scope Scope, tags []string) (selection, error) {
	if tags == nil {
		if !validScope(scope) {
			return selection{}, ErrInvalid
		}
		return selection{instanceID: scope.InstanceID, tags: []string{scope.InboundTag}}, nil
	}
	if scope.InboundTag != "" || !validName(scope.InstanceID, 63) || len(tags) == 0 || len(tags) > MaxSyncInbounds {
		return selection{}, ErrInvalid
	}
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		if !validName(tag, 1024) || seen[tag] {
			return selection{}, ErrInvalid
		}
		seen[tag] = true
	}
	return selection{instanceID: scope.InstanceID, tags: tags, explicit: true}, nil
}

func (s selection) changes(accounts []AccountInput, full bool) ([][]change, error) {
	if accounts == nil || len(accounts) > MaxSyncAccounts {
		return nil, ErrInvalid
	}
	groups := make([][]change, len(s.tags))
	selected := make(map[string]int, len(s.tags))
	for i, tag := range s.tags {
		selected[tag] = i
	}
	assignmentCount := 0
	for _, input := range accounts {
		if !s.explicit {
			credential, err := input.selected()
			if err != nil || input.Assignments != nil || (full && credential == nil) {
				return nil, ErrInvalid
			}
			groups[0] = append(groups[0], change{input.Email, credential})
			continue
		}
		if !validName(input.Email, 1024) || input.UUID != nil || input.Flow != nil || input.Credential != nil {
			return nil, ErrInvalid
		}
		assignmentCount += len(input.Assignments)
		if assignmentCount > MaxSyncAccounts {
			return nil, ErrInvalid
		}
		credentials := make([]*Credential, len(s.tags))
		for i := range input.Assignments {
			assignment := &input.Assignments[i]
			index, exists := selected[assignment.InboundTag]
			if !exists || credentials[index] != nil || !assignment.Credential.valid() {
				return nil, ErrInvalid
			}
			credentials[index] = &assignment.Credential
		}
		for i, credential := range credentials {
			groups[i] = append(groups[i], change{input.Email, credential})
		}
	}
	// Keep the original VLESS wire validation independent of execution readiness.
	if !s.explicit && legacyConflicts(groups[0]) {
		return nil, ErrInvalid
	}
	return groups, nil
}

func legacyConflicts(changes []change) bool {
	hasVLESS := false
	for _, change := range changes {
		if change.credential != nil {
			if change.credential.Protocol != VLESS {
				return false
			}
			hasVLESS = true
		}
	}
	if !hasVLESS {
		return false
	}
	names := make(map[string]bool, len(changes))
	ids := make(map[IDKey]bool, len(changes))
	for _, change := range changes {
		name := NameKey(change.email)
		if names[name] {
			return true
		}
		names[name] = true
		if change.credential != nil {
			id, _ := ParseUUID(change.credential.UUID)
			if ids[id.Key()] {
				return true
			}
			ids[id.Key()] = true
		}
	}
	return false
}
