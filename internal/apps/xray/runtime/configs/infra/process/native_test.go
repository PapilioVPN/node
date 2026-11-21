//go:build integration && linux

package process

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	// A controlled converter child proves cancellation/admission and private
	// resources. Real Xray acceptance below is separate native evidence.
	if len(os.Args) == 6 && os.Args[1] == "convert" && os.Args[2] == "pb" {
		candidate := os.Args[5]
		info, err := os.Stat(candidate)
		if err != nil || info.Mode().Perm() != 0600 {
			os.Exit(25)
		}
		dir, err := os.Stat(filepath.Dir(candidate))
		if err != nil || dir.Mode().Perm() != 0700 {
			os.Exit(25)
		}
		if os.Getenv("XRAY_PARENT_SECRET") != "" {
			os.Exit(25)
		}
		if err := os.WriteFile(candidate+".started", nil, 0600); err != nil {
			os.Exit(25)
		}
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}

func TestOwnerCloseCancelsAndJoinsActualNativeAssetRead(t *testing.T) {
	for _, operation := range []string{"validation", "compilation"} {
		t.Run(operation, func(t *testing.T) { ownerCloseNativeAssetRead(t, operation) })
	}
}
func ownerCloseNativeAssetRead(t *testing.T, operation string) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	fifo := filepath.Join(work, "certificate.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	a, _ := NewAdmission(1)
	b, _ := New(Config{Binary: binary, Directory: work, WorkDirectory: work}, a)
	data, _ := json.Marshal(map[string]any{"outbounds": []any{map[string]any{"protocol": "freedom", "streamSettings": map[string]any{"security": "tls", "tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": fifo, "usage": "verify"}}}}}}})
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var err error
		if operation == "compilation" {
			_, err = b.Compile(parent, []domain.Source{{Name: "blocked.json", Content: data}})
		} else {
			_, err = b.Validate(parent, domain.Candidate{Format: domain.JSON, Content: data})
		}
		done <- err
	}()
	defer func() {
		cancel()
		ctx, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := a.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	// Opening the writer succeeds only after the actual converter has opened
	// the certificate FIFO. Keep it open/empty to hold a real native asset read.
	writer := -1
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for writer < 0 {
		writer, err = unix.Open(fifo, unix.O_WRONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.ENXIO) {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			t.Fatalf("native build ended before asset read: %v", err)
		case <-parent.Done():
			t.Fatal("native certificate read did not start")
		case <-tick.C:
		}
	}
	defer unix.Close(writer)
	stop, cancelStop := context.WithTimeout(context.Background(), time.Second)
	defer cancelStop()
	if err := a.Close(stop); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("owner cancellation lost: %v", err)
	}
	if parent.Err() != nil {
		t.Fatal("owner close canceled the caller's own context")
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || entries[0].Name() != "certificate.fifo" {
		t.Fatalf("converter resources leaked: %v %v", entries, err)
	}
}

func TestActualNativeFormatsRejectionAndNoRuntimeEffects(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	a, _ := NewAdmission(2)
	b, err := New(Config{Binary: binary, Directory: work, WorkDirectory: work}, a)
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(work, "must-not-be-opened.log")
	json := `{"log":{"access":"` + log + `","error":"` + log + `"},"outbounds":[{"protocol":"freedom"}]}`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, test := range []struct {
		format  domain.Format
		content string
		valid   bool
	}{
		{domain.JSON, json, true}, {domain.JSON, "// native comments\n" + json, true},
		{domain.YAML, "log:\n  loglevel: none\noutbounds:\n  - protocol: freedom\n", true},
		{domain.TOML, "[log]\nlevel = 'none'\n[[outbounds]]\nprotocol = 'freedom'\n", true},
		{domain.JSON, `{"inbounds":[{"protocol":"missing-native-protocol"}]}`, false},
		{domain.JSON, `{"inbounds":`, false},
		{domain.Protobuf, "invalid-protobuf", false},
		{domain.Protobuf, "", true},
	} {
		result, err := b.Validate(ctx, domain.Candidate{Format: test.format, Content: []byte(test.content)})
		if err != nil || result.Valid != test.valid || (!test.valid && result.Failure == nil) {
			t.Fatalf("%s valid=%v: %+v %v", test.format, test.valid, result, err)
		}
		entries, err := os.ReadDir(work)
		if err != nil || len(entries) != 0 {
			t.Fatalf("candidate or runtime log leaked: %v %v", entries, err)
		}
	}
	// A protobuf produced by the selected executable must be accepted too.
	source := filepath.Join(t.TempDir(), "candidate.json")
	compiled := filepath.Join(t.TempDir(), "candidate.pb")
	if err := os.WriteFile(source, []byte(json), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "convert", "pb", "-o", compiled, source)
	command.Env = []string{}
	command.Dir = work
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(compiled)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Validate(ctx, domain.Candidate{Format: domain.Protobuf, Content: data})
	if err != nil || !result.Valid {
		t.Fatalf("protobuf: %+v %v", result, err)
	}
}

func TestBoundedAdmissionCancellationJoinAndPrivateCleanup(t *testing.T) {
	t.Setenv("XRAY_PARENT_SECRET", "must-not-leak")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	a, _ := NewAdmission(1)
	b, _ := New(Config{Binary: binary, Directory: work, WorkDirectory: work}, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := b.Validate(ctx, domain.Candidate{Format: domain.JSON, Content: []byte(`{}`)})
		done <- err
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("converter did not join")
		}
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		started, _ := filepath.Glob(filepath.Join(work, "candidate-*", "*.started"))
		if len(started) == 1 {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("controlled converter exited before admission check: %v", err)
		case <-deadline.C:
			t.Fatal("controlled converter did not start")
		case <-tick.C:
		}
	}
	if _, err := b.Validate(context.Background(), domain.Candidate{Format: domain.JSON, Content: []byte(`{}`)}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("overload was not rejected: %v", err)
	}
	if _, err := b.Compile(context.Background(), []domain.Source{{Name: "base.json", Content: []byte(`{}`)}}); !errors.Is(err, domain.ErrCapacity) {
		t.Fatalf("compilation did not share validation capacity: %v", err)
	}
	cancel()
	err = <-done
	done <- err
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 || len(a.slots) != 0 {
		t.Fatalf("unfinished converter resources: %v %v", entries, err)
	}
	// A failed spawn must stay an operation error, never a rejected candidate.
	b.config.Binary = filepath.Join(work, "missing-binary")
	result, err := b.Validate(context.Background(), domain.Candidate{Format: domain.JSON, Content: []byte(`{}`)})
	if err == nil || result.Failure != nil || result.Valid || !strings.Contains(err.Error(), "missing-binary") {
		t.Fatalf("spawn: %+v %v", result, err)
	}
}
