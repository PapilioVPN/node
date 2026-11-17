//go:build integration && linux

package sys_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/identity/infra/sys"
)

func TestReadActualLinuxIdentity(t *testing.T) {
	identity, err := (sys.Reader{}).Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []struct {
		path string
		got  string
	}{
		{"/proc/sys/kernel/ostype", identity.OperatingSystem},
		{"/proc/sys/kernel/osrelease", identity.Release},
		{"/proc/sys/kernel/version", identity.Version},
	} {
		contents, err := os.ReadFile(field.path)
		if err != nil {
			t.Fatal(err)
		}
		if field.got != strings.TrimSuffix(string(contents), "\n") {
			t.Fatalf("%s differs from observed kernel facts: %q", field.path, field.got)
		}
	}
	if identity.Hostname != hostname || identity.Machine == "" {
		t.Fatalf("incorrect native identity: %+v", identity)
	}
}

func TestCanceledReadReturnsNoIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	identity, err := (sys.Reader{}).Read(ctx)
	if !errors.Is(err, context.Canceled) || identity != (domain.Identity{}) {
		t.Fatalf("canceled read: %+v, %v", identity, err)
	}
}
