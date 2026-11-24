package native

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"golang.org/x/sys/unix"
)

func TestCheckpointStoreScope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	store := &CheckpointStore{Path: path}
	if err := store.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	second := &CheckpointStore{Path: path}
	if err := second.Open(t.Context()); !errors.Is(err, unix.EWOULDBLOCK) {
		t.Fatalf("second owner got %v, expected directory lock conflict", err)
	}
	intent := lifecycle.Checkpoint{Link: lifecycle.Link{Name: "wg0", Alias: "papiliovpn-node:91e802b0-7268-4a97-9cec-5b5a8db38a5a"}}
	if err := store.Save(intent); err != nil {
		t.Fatal(err)
	}
	namespace := store.namespace
	store.namespace = "net:[0]"
	if _, err := store.Load(); err == nil {
		t.Fatal("loaded checkpoint from another network namespace")
	}
	store.namespace = namespace
	if got, err := store.Load(); err != nil || got.Link != intent.Link {
		t.Fatalf("intent after rejected recovery = %v, %v", got, err)
	}
}
