package logind

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/godbus/dbus/v5"
)

type callerFixture struct {
	rows   []extendedSession
	basic  []basicSession
	legacy bool
	calls  int
	cause  error
}

func (fixture *callerFixture) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	fixture.calls++
	if fixture.cause != nil {
		return &dbus.Call{Err: fixture.cause}
	}
	if method == destination+".Manager.ListSessionsEx" {
		if fixture.legacy {
			return &dbus.Call{Err: dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownMethod"}}
		}
		return &dbus.Call{Body: []any{fixture.rows}}
	}
	if method == destination+".Manager.ListSessions" {
		return &dbus.Call{Body: []any{fixture.basic}}
	}
	panic("unexpected native call")
}
func TestNativeBulkMethodGrowthAndKnownZeroPresence(t *testing.T) {
	for _, size := range []int{0, 1, 64, 256, 4096} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			fixture := &callerFixture{rows: make([]extendedSession, size)}
			for i := range fixture.rows {
				fixture.rows[i] = extendedSession{ID: fmt.Sprintf("s%05d", size-i), User: "test", Path: "/session/one"}
			}
			page, err := list(context.Background(), fixture, domain.Selection{Limit: 128})
			if err != nil || fixture.calls != 1 || len(page.Sessions) != min(size, 128) || !page.Extended || (size > 128) != (page.NextOffset != nil) {
				t.Fatalf("size=%d calls=%d page=%+v err=%v", size, fixture.calls, page, err)
			}
			if size > 0 && (page.Sessions[0].ID != "s00001" || page.Sessions[0].LeaderPID == nil || *page.Sessions[0].LeaderPID != 0 || page.Sessions[0].Idle == nil || *page.Sessions[0].Idle || page.Sessions[0].Terminal == nil || *page.Sessions[0].Terminal != "") {
				t.Fatalf("lost zero presence: %+v", page.Sessions[0])
			}
		})
	}
	fixture := &callerFixture{legacy: true, basic: []basicSession{{ID: "s1", User: "test"}}}
	page, err := list(context.Background(), fixture, domain.Selection{Limit: 1})
	if err != nil || fixture.calls != 2 || page.Extended || len(page.Sessions) != 1 || page.Sessions[0].LeaderPID != nil || page.Sessions[0].Idle != nil {
		t.Fatalf("invented legacy properties: %+v %v", page, err)
	}
}
func TestNativeErrorsAndDuplicateIdentities(t *testing.T) {
	for _, failure := range []struct {
		name     string
		expected error
	}{{"org.freedesktop.DBus.Error.AccessDenied", domain.ErrPermissionDenied}, {"org.freedesktop.login1.NoSuchSession", domain.ErrNotFound}, {"org.freedesktop.DBus.Error.ServiceUnknown", domain.ErrUnavailable}} {
		cause := dbus.Error{Name: failure.name}
		fixture := &callerFixture{cause: cause}
		if _, err := list(context.Background(), fixture, domain.Selection{Limit: 1}); !errors.Is(err, failure.expected) || fixture.calls != 1 {
			t.Fatalf("%v calls=%d", err, fixture.calls)
		}
	}
	fixture := &callerFixture{rows: []extendedSession{{ID: "s1"}, {ID: "s1"}}}
	if _, err := list(context.Background(), fixture, domain.Selection{Limit: 1}); err == nil {
		t.Fatal("accepted duplicate sessions")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := nativeError(ctx, errors.New("native cause")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestDetailsKeepNativeTypesAndOptionalAbsence(t *testing.T) {
	values := map[string]dbus.Variant{
		"Id": dbus.MakeVariant("s1"), "Name": dbus.MakeVariant("test"),
		"User": dbus.MakeVariant(struct {
			UID  uint32
			Path dbus.ObjectPath
		}{0, "/user/root"}),
		"Seat": dbus.MakeVariant(struct {
			ID   string
			Path dbus.ObjectPath
		}{"", "/"}),
		"Timestamp": dbus.MakeVariant(uint64(9007199254740993)), "Remote": dbus.MakeVariant(false), "Leader": dbus.MakeVariant(uint32(0)),
	}
	result, err := details(values)
	if err != nil || result.Summary.UID != 0 || result.StartedUnixUS == nil || *result.StartedUnixUS != 9007199254740993 || result.Remote == nil || *result.Remote || result.Summary.Terminal != nil || result.Summary.LeaderPID == nil {
		t.Fatalf("%+v %v", result, err)
	}
	values["Leader"] = dbus.MakeVariant(uint64(1 << 40))
	if _, err := details(values); err == nil {
		t.Fatal("narrowed an invalid native property type")
	}
	delete(values, "Leader")
	delete(values, "User")
	if _, err := details(values); err == nil {
		t.Fatal("invented missing native identity")
	}
}
