package logind

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/godbus/dbus/v5"
)

// optional decodes one scalar D-Bus property without coercing its native type.
// Absent and present-zero remain distinct; this is a protocol primitive only.
func optional[T any](values map[string]dbus.Variant, name string) (*T, error) {
	variant, exists := values[name]
	if !exists {
		return nil, nil
	}
	value, ok := variant.Value().(T)
	if !ok {
		return nil, fmt.Errorf("invalid native property type: %s", name)
	}
	return &value, nil
}
func details(values map[string]dbus.Variant) (domain.Details, error) {
	var result domain.Details
	id, err := optional[string](values, "Id")
	if err != nil {
		return result, err
	}
	name, err := optional[string](values, "Name")
	if err != nil {
		return result, err
	}
	user, hasUser := values["User"]
	seat, hasSeat := values["Seat"]
	if id == nil || name == nil || !hasUser || !hasSeat || *id == "" || user.Signature().String() != "(uo)" || seat.Signature().String() != "(so)" {
		return result, errors.New("missing native session identity")
	}
	var nativeUser struct {
		UID  uint32
		Path dbus.ObjectPath
	}
	var nativeSeat struct {
		ID   string
		Path dbus.ObjectPath
	}
	if err := user.Store(&nativeUser); err != nil {
		return result, err
	}
	if err := seat.Store(&nativeSeat); err != nil {
		return result, err
	}
	result.Summary = domain.Summary{ID: *id, UID: nativeUser.UID, User: *name, Seat: nativeSeat.ID}
	result.Summary.LeaderPID, err = optional[uint32](values, "Leader")
	if err != nil {
		return domain.Details{}, err
	}
	result.Summary.Class, err = optional[string](values, "Class")
	if err != nil {
		return domain.Details{}, err
	}
	result.Summary.Terminal, err = optional[string](values, "TTY")
	if err != nil {
		return domain.Details{}, err
	}
	result.Summary.Idle, err = optional[bool](values, "IdleHint")
	if err != nil {
		return domain.Details{}, err
	}
	result.Summary.IdleSinceMonotonicUS, err = optional[uint64](values, "IdleSinceHintMonotonic")
	if err != nil {
		return domain.Details{}, err
	}
	result.Remote, err = optional[bool](values, "Remote")
	if err != nil {
		return domain.Details{}, err
	}
	result.RemoteHost, err = optional[string](values, "RemoteHost")
	if err != nil {
		return domain.Details{}, err
	}
	result.RemoteUser, err = optional[string](values, "RemoteUser")
	if err != nil {
		return domain.Details{}, err
	}
	result.StartedUnixUS, err = optional[uint64](values, "Timestamp")
	if err != nil {
		return domain.Details{}, err
	}
	result.StartedMonotonicUS, err = optional[uint64](values, "TimestampMonotonic")
	if err != nil {
		return domain.Details{}, err
	}
	result.Type, err = optional[string](values, "Type")
	if err != nil {
		return domain.Details{}, err
	}
	result.State, err = optional[string](values, "State")
	if err != nil {
		return domain.Details{}, err
	}
	result.Active, err = optional[bool](values, "Active")
	if err != nil {
		return domain.Details{}, err
	}

	for _, value := range []string{result.Summary.ID, result.Summary.User, result.Summary.Seat} {
		if !utf8.ValidString(value) {
			return domain.Details{}, errors.New("invalid native session text")
		}
	}
	for _, value := range []*string{result.Summary.Class, result.Summary.Terminal, result.RemoteHost, result.RemoteUser, result.Type, result.State} {
		if value != nil && !utf8.ValidString(*value) {
			return domain.Details{}, errors.New("invalid native session property text")
		}
	}
	return result, nil
}
