// Package logind adapts native read-only systemd login1 methods over private D-Bus connections.
package logind

import (
	"context"
	"errors"
	"net"
	"os"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/godbus/dbus/v5"
)

const destination = "org.freedesktop.login1"
const managerPath = dbus.ObjectPath("/org/freedesktop/login1")

type Reader struct {
	SocketPath string
	Timeout    time.Duration
}

func (reader Reader) connect(ctx context.Context) (*dbus.Conn, error) {
	// Native Dial does not accept a context. Use net.Dialer and the SDK's native
	// established-connection constructor; no environment-selected bus or cookie auth.
	raw, err := (&net.Dialer{}).DialContext(ctx, "unix", reader.SocketPath)
	if err != nil {
		return nil, nativeError(ctx, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := raw.SetDeadline(deadline); err != nil {
			raw.Close()
			return nil, err
		}
	}
	conn, err := dbus.NewConn(raw, dbus.WithContext(ctx))
	if err != nil {
		raw.Close()
		return nil, nativeError(ctx, err)
	}
	if err := conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err != nil {
		conn.Close()
		return nil, nativeError(ctx, err)
	}
	if err := conn.Hello(); err != nil {
		conn.Close()
		return nil, nativeError(ctx, err)
	}
	return conn, nil
}

func (reader Reader) List(ctx context.Context, input domain.Selection) (domain.Page, error) {
	ctx, cancel := context.WithTimeout(ctx, reader.Timeout)
	defer cancel()
	conn, err := reader.connect(ctx)
	if err != nil {
		return domain.Page{}, err
	}
	defer conn.Close()
	return list(ctx, conn.Object(destination, managerPath), input)
}
func (reader Reader) Read(ctx context.Context, id string) (domain.Details, error) {
	ctx, cancel := context.WithTimeout(ctx, reader.Timeout)
	defer cancel()
	conn, err := reader.connect(ctx)
	if err != nil {
		return domain.Details{}, err
	}
	defer conn.Close()
	var path dbus.ObjectPath
	if err := conn.Object(destination, managerPath).CallWithContext(ctx, destination+".Manager.GetSession", 0, id).Store(&path); err != nil {
		return domain.Details{}, nativeError(ctx, err)
	}
	if !path.IsValid() {
		return domain.Details{}, errors.New("invalid native session path")
	}
	call := conn.Object(destination, path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, destination+".Session")
	if call.Err != nil {
		return domain.Details{}, nativeError(ctx, call.Err)
	}
	// Retain decoded variant signatures. At this SDK pin, generic Store of a
	// variant map reconstructs tuple variants as arrays of variants (av).
	if len(call.Body) != 1 {
		return domain.Details{}, errors.New("invalid native session property response")
	}
	values, ok := call.Body[0].(map[string]dbus.Variant)
	if !ok {
		return domain.Details{}, errors.New("invalid native session property dictionary")
	}
	result, err := details(values)
	if err != nil {
		return domain.Details{}, err
	}
	if result.Summary.ID != id {
		return domain.Details{}, errors.New("native session identity changed")
	}
	if err := ctx.Err(); err != nil {
		return domain.Details{}, err
	}
	return result, nil
}

type caller interface {
	CallWithContext(context.Context, string, dbus.Flags, ...any) *dbus.Call
}
type basicSession struct {
	ID   string
	UID  uint32
	User string
	Seat string
	Path dbus.ObjectPath
}
type extendedSession struct {
	ID            string
	UID           uint32
	User          string
	Seat          string
	Leader        uint32
	Class         string
	Terminal      string
	Idle          bool
	IdleMonotonic uint64
	Path          dbus.ObjectPath
}

func list(ctx context.Context, object caller, input domain.Selection) (domain.Page, error) {
	var extended []extendedSession
	err := object.CallWithContext(ctx, destination+".Manager.ListSessionsEx", 0).Store(&extended)
	rows := make([]domain.Summary, 0, len(extended))
	hasExtended := true
	if err != nil {
		var native dbus.Error
		if !errors.As(err, &native) || native.Name != "org.freedesktop.DBus.Error.UnknownMethod" {
			return domain.Page{}, nativeError(ctx, err)
		}
		var basic []basicSession
		if err := object.CallWithContext(ctx, destination+".Manager.ListSessions", 0).Store(&basic); err != nil {
			return domain.Page{}, nativeError(ctx, err)
		}
		if len(basic) > 16384 {
			return domain.Page{}, domain.ErrResourceExhausted
		}
		hasExtended = false
		for _, row := range basic {
			rows = append(rows, domain.Summary{ID: row.ID, UID: row.UID, User: row.User, Seat: row.Seat})
		}
	} else {
		if len(extended) > 16384 {
			return domain.Page{}, domain.ErrResourceExhausted
		}
		for _, row := range extended {
			rows = append(rows, domain.Summary{ID: row.ID, UID: row.UID, User: row.User, Seat: row.Seat, LeaderPID: &row.Leader, Class: &row.Class, Terminal: &row.Terminal, Idle: &row.Idle, IdleSinceMonotonicUS: &row.IdleMonotonic})
		}
	}
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return domain.Page{}, err
		}
		if row.ID == "" || len(row.ID) > 255 {
			return domain.Page{}, errors.New("invalid native session identity")
		}
		for _, value := range []string{row.ID, row.User, row.Seat} {
			if !utf8.ValidString(value) {
				return domain.Page{}, errors.New("invalid native session text")
			}
		}
		if row.Class != nil && (!utf8.ValidString(*row.Class) || !utf8.ValidString(*row.Terminal)) {
			return domain.Page{}, errors.New("invalid native extended session text")
		}
		if _, exists := seen[row.ID]; exists {
			return domain.Page{}, errors.New("duplicate native session identity")
		}
		seen[row.ID] = struct{}{}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	start := min(int(input.Offset), len(rows))
	end := min(start+int(input.Limit), len(rows))
	page := domain.Page{Sessions: rows[start:end], Extended: hasExtended}
	if end < len(rows) {
		next := uint32(end)
		page.NextOffset = &next
	}
	return page, nil
}

func nativeError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), err)
	}
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
		return errors.Join(context.DeadlineExceeded, err)
	}
	var native dbus.Error
	if errors.As(err, &native) {
		switch native.Name {
		case "org.freedesktop.login1.NoSuchSession", "org.freedesktop.DBus.Error.UnknownObject":
			return errors.Join(domain.ErrNotFound, err)
		case "org.freedesktop.DBus.Error.AccessDenied", "org.freedesktop.DBus.Error.AuthFailed":
			return errors.Join(domain.ErrPermissionDenied, err)
		}
	}
	if errors.Is(err, os.ErrPermission) {
		return errors.Join(domain.ErrPermissionDenied, err)
	}
	return errors.Join(domain.ErrUnavailable, err)
}
