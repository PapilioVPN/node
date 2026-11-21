//go:build integration && linux

package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func openStore(t *testing.T, directory string) *Store {
	t.Helper()
	store, err := New(directory, "edge-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return store
}

func TestPrivateSourcesCASPersistenceAndRecovery(t *testing.T) {
	directory := t.TempDir()
	store := openStore(t, directory)
	ctx := context.Background()
	metadata, err := store.Metadata(ctx)
	if err != nil || metadata.Revision != 0 {
		t.Fatal("new store invented desired state")
	}
	sources := []domain.Source{{Name: "base.json", Content: []byte(`{"env":{"xray.location.cert":"private"}}`)}, {Name: "tail-extra.yaml", Content: []byte("log:\n  loglevel: none\n")}}
	result, err := store.Write(ctx, 0, sources)
	if err != nil || !result.Published || !result.Durable || result.Metadata.Revision != 1 {
		t.Fatalf("commit: %+v %v", result, err)
	}
	sources[0].Content[0] = '!'
	desired, err := store.Read(ctx)
	if err != nil || desired.Metadata.Revision != 1 || !desired.Metadata.Durable || desired.Sources[0].Content[0] != '{' || desired.Sources[1].Name != "tail-extra.yaml" {
		t.Fatalf("read: %+v %v", desired.Metadata, err)
	}
	desired.Sources[0].Content[0] = '!'
	if _, err := store.Write(ctx, 0, []domain.Source{{Name: "empty.pb"}}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale revision overwrote current source")
	}
	info, err := os.Stat(filepath.Join(directory, recordName))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("source record is not private")
	}
	competing, _ := New(directory, "edge-a")
	if err := competing.Open(ctx); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal("second writer acquired the same state")
	}
	if err := store.Close(ctx); err != nil {
		t.Fatal(err)
	}
	restored := openStore(t, directory)
	desired, err = restored.Read(ctx)
	if err != nil || desired.Metadata.Revision != 1 || desired.Sources[0].Content[0] != '{' || !desired.Metadata.Durable {
		t.Fatal("source bytes/environment lost after reopening")
	}
	result, err = restored.Write(ctx, 1, []domain.Source{{Name: "empty.pb"}})
	if err != nil || result.Metadata.Revision != 2 {
		t.Fatalf("empty native protobuf: %+v %v", result, err)
	}
	entries, _ := os.ReadDir(directory)
	if len(entries) != 2 {
		t.Fatalf("temporary state files leaked: %v", entries)
	}
}

func TestPublishedSyncFailureAndLostRenameAckKeepHonestEffects(t *testing.T) {
	for _, failure := range []string{"directory_sync", "rename_reply"} {
		t.Run(failure, func(t *testing.T) {
			store := openStore(t, t.TempDir())
			cause := errors.New("controlled native acknowledgement failure")
			if failure == "directory_sync" {
				store.syncRoot = func(*os.Root) error { return cause }
			} else {
				store.publish = func(root *os.Root, from, to string) error {
					if err := root.Rename(from, to); err != nil {
						return err
					}
					return cause
				}
			}
			result, err := store.Write(context.Background(), 0, []domain.Source{{Name: "empty.pb"}})
			if !errors.Is(err, domain.ErrDurabilityUnknown) || result.Durable || result.Metadata.Revision != 1 {
				t.Fatalf("effects lost: %+v %v", result, err)
			}
			if failure == "directory_sync" && !result.Published {
				t.Fatal("known publication became not-applied")
			}
			if failure == "rename_reply" && !result.PublicationUnknown {
				t.Fatal("lost rename ack became known no effect")
			}
			desired, err := store.Read(context.Background())
			if err != nil || desired.Metadata.Revision != 1 || desired.Metadata.Durable {
				t.Fatal("readback invented durability or served stale state")
			}
			store.syncRoot = syncDirectory
			metadata, err := store.Confirm(context.Background(), 1)
			if err != nil || !metadata.Durable {
				t.Fatal("explicit durability confirmation failed")
			}
		})
	}
}

func TestConfirmedPublicationSurvivesLateCancellation(t *testing.T) {
	store := openStore(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store.syncRoot = func(root *os.Root) error { err := syncDirectory(root); cancel(); return err }
	result, err := store.Write(ctx, 0, []domain.Source{{Name: "empty.pb"}})
	if err != nil || !result.Durable || !result.Published || ctx.Err() == nil {
		t.Fatalf("confirmed write erased by cancellation: %+v %v", result, err)
	}
}

func TestCorruptOrForeignStateDoesNotBecomeSuccessfulAbsence(t *testing.T) {
	for _, data := range []string{`{}`, `{"schema":99}`, `{"schema":1,"instance_id":"other","revision":"1"}`, `not json`} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, recordName), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		store, _ := New(directory, "edge-a")
		if err := store.Open(context.Background()); err == nil {
			store.Close(context.Background())
			t.Fatal("corrupt state accepted")
		}
	}
	missing := filepath.Join(t.TempDir(), "missing")
	store, _ := New(missing, "edge-a")
	if err := store.Open(context.Background()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("state root was provisioned implicitly")
	}
}
