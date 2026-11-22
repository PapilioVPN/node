package validators

import (
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
)

func TestOpaqueIdentityAndSupportedBounds(t *testing.T) {
	for _, name := range []string{" ", " A>>>B/نام ", strings.Repeat("x", 4096)} {
		if err := Selection(domain.Selection{Scope: domain.User, Name: name, Direction: domain.Uplink}); err != nil {
			t.Fatal("valid opaque identity rejected", err)
		}
	}
	for _, name := range []string{"", "x\x00y", "\xff", strings.Repeat("x", 4097)} {
		if err := Selection(domain.Selection{Scope: domain.Inbound, Name: name, Direction: domain.Downlink}); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal("invalid identity accepted", err)
		}
	}
}
