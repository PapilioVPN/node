package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type failedUpdateControl struct {
	Control
	link      status.Interface
	updateErr error
	updates   int
	removed   bool
}

func (c *failedUpdateControl) Create(_ context.Context, owner Link, cfg Config) (status.Interface, error) {
	c.link = status.Interface{Name: owner.Name, Index: 2, Alias: owner.Alias, Up: true, MTU: cfg.MTU}
	return c.link, nil
}

func (c *failedUpdateControl) UpdateMTU(_ context.Context, _ Link, _, _ uint32) (status.Interface, error) {
	c.updates++
	return status.Interface{}, c.updateErr
}

func (c *failedUpdateControl) Remove(_ context.Context, owner Link) error {
	if owner.Index != c.link.Index || owner.Alias != c.link.Alias {
		return ErrConflict
	}
	c.removed = true
	return nil
}

func TestLiveMTUFailure(t *testing.T) {
	for _, tc := range []struct {
		name          string
		updateErr     error
		keepConfirmed bool
	}{
		{"confirmed previous MTU", status.ErrUnavailable, true},
		{"unknown outcome", ErrUnconfirmed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			control := &failedUpdateControl{updateErr: tc.updateErr}
			service := New("wg0", control, nil, nil)
			initial, err := service.Start(t.Context(), "wg0", []byte(validSource))
			if err != nil {
				t.Fatal(err)
			}
			proposed := []byte(strings.TrimSuffix(validSource, "}") + `,"mtu":1380}`)
			if _, err := service.Start(t.Context(), "wg0", proposed); !errors.Is(err, tc.updateErr) {
				t.Fatalf("Start returned %v, want %v", err, tc.updateErr)
			}
			want := ""
			if tc.keepConfirmed {
				want = initial.AppliedSHA256
			}
			if got := service.AppliedHash(control.link); got != want {
				t.Fatalf("applied hash = %q, want %q", got, want)
			}
			if !tc.keepConfirmed {
				if _, err := service.Start(t.Context(), "wg0", proposed); !errors.Is(err, ErrConflict) || control.updates != 1 {
					t.Fatalf("another update after an unknown outcome = %v, native calls = %d", err, control.updates)
				}
			}
			if err := service.Stop(t.Context(), "wg0"); err != nil || !control.removed {
				t.Fatalf("Stop failed to clean up the owned interface: %v", err)
			}
		})
	}
}
