package services

import (
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
)

func TestOpaqueIdentityAndLimits(t *testing.T) {
	for _, email := range []string{" a>>>b ", strings.Repeat("x", 4096)} {
		if err := validateSelection(domain.Selection{InstanceID: "a", Email: email}); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []domain.Selection{{InstanceID: "../a", Email: "x"}, {InstanceID: "a"}, {InstanceID: "a", Email: strings.Repeat("x", 4097)}, {InstanceID: "a", Email: "\x00"}, {InstanceID: "a", Email: "\xff"}} {
		if validateSelection(s) == nil {
			t.Fatal(s)
		}
	}
}
