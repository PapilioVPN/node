//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "run" && os.Args[2] == "-c" {
		data, err := os.ReadFile(os.Args[3])
		if err != nil {
			os.Exit(24)
		}
		if string(data) == "exit" {
			os.Exit(23)
		}
		if os.Getenv("NODE_PARENT_SECRET") != "" {
			os.Exit(25)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		if err := os.WriteFile(os.Args[3]+".ready", nil, 0600); err != nil {
			os.Exit(26)
		}
		for {
			<-signals
			if string(data) != "ignore" {
				syscall.Exit(0)
			}
		}
	}
	os.Exit(m.Run())
}

func fixtureProcess(t *testing.T, mode string) *Process {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(config, []byte(mode), 0600); err != nil {
		t.Fatal(err)
	}
	process, err := New(Config{Binary: binary, Configuration: config, StopTimeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := process.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return process
}

func TestOwnedProcessStopRestartAndNoEnvironmentLeak(t *testing.T) {
	t.Setenv("NODE_PARENT_SECRET", "must-not-leak")
	p := fixtureProcess(t, "normal")
	for generation := uint64(1); generation <= 2; generation++ {
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		waitFor(t, func() bool { _, err := os.Stat(p.config.Configuration + ".ready"); return err == nil })
		got := p.Observe()
		if got.State != "running" || got.Generation != generation {
			t.Fatalf("running: %+v", got)
		}
		if err := p.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if p.Observe().Generation != generation {
			t.Fatal("start duplicated execution")
		}
		if err := p.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		got = p.Observe()
		if got.State != "stopped" || got.ExitCode == nil || *got.ExitCode != 0 || got.Forced {
			t.Fatalf("stop: %+v", got)
		}
		os.Remove(p.config.Configuration + ".ready")
	}
}

func TestForcedStopAndUnexpectedExitRemainObservable(t *testing.T) {
	p := fixtureProcess(t, "ignore")
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(p.config.Configuration + ".ready"); return err == nil })
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !p.Observe().Forced || p.Observe().State != "stopped" {
		t.Fatalf("forced: %+v", p.Observe())
	}
	exited := fixtureProcess(t, "exit")
	if err := exited.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return exited.Observe().State == "exited" })
	if got := exited.Observe(); got.ExitCode == nil || *got.ExitCode != 23 || got.Failure == nil {
		t.Fatalf("exit: %+v", got)
	}
}

func TestCanceledStartDoesNotSpawnAndClosePreventsRestart(t *testing.T) {
	p := fixtureProcess(t, "normal")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Start(ctx); err == nil {
		t.Fatal("canceled start admitted")
	}
	if p.Observe().Generation != 0 {
		t.Fatal("canceled start spawned")
	}
	if err := p.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(context.Background()); err == nil {
		t.Fatal("closed instance restarted")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal("process did not reach expected state")
		case <-tick.C:
		}
	}
}

func TestCanceledStopRetainsOwnershipUntilExitAndCloseJoins(t *testing.T) {
	p := fixtureProcess(t, "ignore")
	start, cancelStart := context.WithCancel(context.Background())
	if err := p.Start(start); err != nil {
		t.Fatal(err)
	}
	cancelStart()
	waitFor(t, func() bool { _, err := os.Stat(p.config.Configuration + ".ready"); return err == nil })
	if p.Observe().State != "running" {
		t.Fatal("successful start inherited caller cancellation")
	}
	stop, cancelStop := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancelStop()
	err := p.Stop(stop)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := p.Close(ctx); err != nil {
		t.Fatal(err)
	}
	result := p.Observe()
	if result.State != "stopped" || result.ExitedAt == nil || !result.Forced {
		t.Fatalf("unjoined stop: %+v", result)
	}
	// Public observations cannot mutate the resource owner's retained evidence.
	*result.ExitCode = 999
	if *p.Observe().ExitCode == 999 {
		t.Fatal("snapshot borrowed mutable state")
	}
}
