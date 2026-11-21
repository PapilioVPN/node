//go:build linux

package process

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
)

type sourceFunc func(context.Context) (*Input, error)

func (f sourceFunc) Open(ctx context.Context) (*Input, error) { return f(ctx) }
func TestManagedSourceLeaseSurvivesStartRequestAndJoinsExit(t *testing.T) {
	p := fixtureProcess(t, "baseline")
	root := t.TempDir()
	var staged string
	var releases atomic.Int32
	p.config.Source = sourceFunc(func(context.Context) (*Input, error) {
		var err error
		staged, err = os.MkdirTemp(root, "lease-")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(staged, "base.json"), []byte("normal"), 0600); err != nil {
			return nil, err
		}
		dir, err := os.Open(staged)
		return &Input{Directory: dir, Names: []string{"base.json"}, Revision: math.MaxUint64, Release: func() error { releases.Add(1); return os.RemoveAll(staged) }}, err
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(staged, "base.json.ready")); return err == nil })
	if releases.Load() != 0 {
		t.Fatal("successful start released execution source too early")
	}
	snapshot := p.Observe()
	if snapshot.ConfigurationRevision == nil || *snapshot.ConfigurationRevision != math.MaxUint64 {
		t.Fatal("managed revision lost")
	}
	*snapshot.ConfigurationRevision = 7
	if *p.Observe().ConfigurationRevision != math.MaxUint64 {
		t.Fatal("observer mutated owned revision")
	}
	if err := p.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) || releases.Load() != 1 {
		t.Fatal("stop did not join source release")
	}
}
func TestFailedSourceCleanupRemainsOwnedAndBlocksAnotherExecution(t *testing.T) {
	p := fixtureProcess(t, "baseline")
	var fail atomic.Bool
	fail.Store(true)
	var opens atomic.Int32
	var staged string
	root := t.TempDir()
	p.config.Source = sourceFunc(func(context.Context) (*Input, error) {
		opens.Add(1)
		var err error
		staged, err = os.MkdirTemp(root, "lease-")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(staged, "base.json"), []byte("normal"), 0600); err != nil {
			return nil, err
		}
		dir, err := os.Open(staged)
		return &Input{Directory: dir, Names: []string{"base.json"}, Revision: 1, Release: func() error {
			if fail.Load() {
				return os.ErrPermission
			}
			return os.RemoveAll(staged)
		}}, err
	})
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(filepath.Join(staged, "base.json.ready")); return err == nil })
	if err := p.Stop(context.Background()); !errors.Is(err, domain.ErrCleanupFailed) {
		t.Fatal(err)
	}
	if p.Observe().CleanupFailure == nil {
		t.Fatal("cleanup failure disappeared")
	}
	if err := p.Start(context.Background()); !errors.Is(err, domain.ErrNotApplied) || !errors.Is(err, domain.ErrCleanupFailed) {
		t.Fatal(err)
	}
	if opens.Load() != 1 || p.Observe().Generation != 1 {
		t.Fatal("failed cleanup admitted a new lease")
	}
	fail.Store(false)
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.Observe().CleanupFailure != nil || p.Observe().Generation != 2 {
		t.Fatal("successful cleanup did not permit new execution")
	}
}
func TestFailedSpawnReleasesManagedSourceWithoutNewGeneration(t *testing.T) {
	p := fixtureProcess(t, "baseline")
	p.config.Binary = filepath.Join(t.TempDir(), "missing")
	var released atomic.Bool
	p.config.Source = sourceFunc(func(context.Context) (*Input, error) {
		dir, err := os.Open(t.TempDir())
		return &Input{Directory: dir, Names: []string{"base.json"}, Revision: 1, Release: func() error { released.Store(true); return nil }}, err
	})
	if err := p.Start(context.Background()); !errors.Is(err, domain.ErrNotApplied) {
		t.Fatal(err)
	}
	if !released.Load() || p.Observe().Generation != 0 {
		t.Fatal("failed spawn leaked its source or fabricated execution")
	}
}
