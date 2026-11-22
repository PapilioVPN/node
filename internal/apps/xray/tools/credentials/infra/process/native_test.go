//go:build integration && linux

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
)

func TestRealNativeBlockedHashCancellationAndClose(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "close"} {
		t.Run(mode, func(t *testing.T) {
			r := nativeRunner(t)
			path := filepath.Join(t.TempDir(), "certificate.fifo")
			if syscall.Mkfifo(path, 0600) != nil {
				t.Fatal("cannot create isolated FIFO")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			execution, release, err := r.acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				defer release()
				raw, err := r.run(execution, "tls", "hash", "--cert", path)
				clear(raw)
				done <- err
			}()
			var writer *os.File
			for {
				writer, err = os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0600)
				if err == nil {
					break
				}
				select {
				case err := <-done:
					t.Fatal("native hash did not block", err)
				case <-ctx.Done():
					t.Fatal("native reader did not open owned FIFO")
				case <-time.After(5 * time.Millisecond):
				}
			}
			defer writer.Close()
			if mode == "close" {
				closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				if r.owner.Close(closeCtx) != nil {
					t.Fatal("owner did not cancel/join real child")
				}
			} else if mode == "cancel" {
				cancel()
			}
			select {
			case err := <-done:
				want := context.Canceled
				if mode == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatal("cancellation classification", err)
				}
			case <-time.After(time.Second):
				t.Fatal("native child did not join")
			}
		})
	}
}
func TestExecutionHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "overflow":
		fmt.Fprint(os.Stdout, strings.Repeat("x", domain.MaxOutputBytes+1))
	case "reject":
		fmt.Fprint(os.Stdout, "fixture-private-output")
		fmt.Fprint(os.Stderr, "fixture-private-diagnostic")
		os.Exit(1)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
func TestControlledChildOutputLimitAndPrivateErrors(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r, owner := testRunner(t)
	r.config.Binary = binary
	for _, test := range []struct {
		mode string
		want error
	}{{"overflow", domain.ErrCapacity}, {"reject", domain.ErrRejected}} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		execution, release, err := r.acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := r.run(execution, "-test.run=^TestExecutionHelper$", "--", test.mode)
		release()
		cancel()
		if !errors.Is(err, test.want) || len(raw) != 0 || strings.Contains(err.Error(), "fixture-private") {
			t.Fatalf("controlled child mode=%s cause=%v output_bytes=%d", test.mode, err, len(raw))
		}
	}
	if owner.active != 0 {
		t.Fatal("child execution leaked capacity")
	}
}
