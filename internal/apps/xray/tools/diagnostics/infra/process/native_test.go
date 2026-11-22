//go:build integration && linux

package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

func TestControlledVersionChildBoundsCancellationAndSlotRelease(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "fixture-version")
	slots := make(chan struct{}, 2)
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(file, []byte("#!/bin/sh\n"+content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("printf '%s\\n' 'Xray 26.11.9 (Xray, Penetrates Everything.) 676eaf6 (go1.27.3 linux/amd64)'\nprintf '%s\\n' 'password-private-key' >&2\n")
	r := Reader{Binary: file, Slots: slots}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := r.Read(ctx)
	if err != nil || value.Build != "676eaf6" || strings.Contains(value.Version, "private") {
		t.Fatal(value, err)
	}
	write("while :; do printf '%s' 'xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx'; done\n")
	if _, err := r.Read(ctx); !errors.Is(err, domain.ErrCapacity) || len(slots) != 0 {
		t.Fatal(err, len(slots))
	}
	write("while :; do :; done\n")
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := r.Read(short); !errors.Is(err, context.DeadlineExceeded) || len(slots) != 0 {
		t.Fatal(err, len(slots))
	}
	slots <- struct{}{}
	slots <- struct{}{}
	if _, err := r.Read(ctx); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
	<-slots
	<-slots
	r.Binary = filepath.Join(dir, "private-missing-binary")
	if _, err := r.Read(ctx); !errors.Is(err, domain.ErrUnavailable) || !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
