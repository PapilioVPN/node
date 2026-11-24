package native

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/protobuf/proto"
)

func TestRecoveryStoreReloadsAcceptedScopeAndRemovals(t *testing.T) {
	dir := privateTestDirectory(t)
	client := New(nil)
	if err := client.OpenRecovery(dir, "primary"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if client != nil {
			_ = client.CloseRecovery(t.Context())
		}
	})
	if other, _, err := openRecoveryStore(dir, "primary"); err == nil {
		other.close()
		t.Fatal("two writers locked the same recovery directory")
	}

	layout := accountLayouts[vlessInboundType]
	data, err := proto.Marshal(&VlessAccount{Id: "03a15cb3-bba3-4b4e-b4c5-2d3fce53077c", Flow: "xtls-rprx-vision"})
	if err != nil {
		t.Fatal(err)
	}
	delta := acceptedInbound{layout: layout, accounts: make(map[string]*User)}
	delta.set("alice", &User{Email: "Alice", Level: 9, Account: &TypedMessage{Type: layout.accountType, Value: data}})
	delta.set("removed", nil)
	full := acceptedInbound{layout: layout, full: true, accounts: make(map[string]*User)}
	state := &acceptedMembership{inbounds: map[string]acceptedInbound{"delta": delta, "full-empty": full}}
	if err := client.accept(state); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, recoveryFilename)
	original, err := os.Stat(path)
	if err != nil || original.Mode().Perm() != 0600 {
		t.Fatalf("recovery file mode: %v, %v", original, err)
	}
	if err := client.accept(state.clone()); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.Stat(path)
	if err != nil || !os.SameFile(original, unchanged) {
		t.Fatal("unchanged account state was rewritten", err)
	}
	if err := client.CloseRecovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	client = nil
	if err := os.WriteFile(filepath.Join(dir, recoveryPending), []byte("unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	if other, _, err := openRecoveryStore(dir, "other"); !errors.Is(err, errInvalidRecoveryFile) {
		if other != nil {
			other.close()
		}
		t.Fatal("state was accepted by a different instance", err)
	}
	reopened, recovered, err := openRecoveryStore(dir, "primary")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if !sameAccepted(state, recovered) {
		t.Fatal("recovered state lost the original credential, full scope, or removal")
	}
	if _, err := os.Stat(filepath.Join(dir, recoveryPending)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unfinished state was not discarded", err)
	}
}

func TestRecoveryStoreRejectsCorruptAndExposedState(t *testing.T) {
	dir := privateTestDirectory(t)
	path := filepath.Join(dir, recoveryFilename)
	state := &acceptedMembership{inbounds: make(map[string]acceptedInbound)}
	data, err := encodeRecovery(state, "primary")
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openRecoveryStore(dir, "primary"); !errors.Is(err, errInvalidRecoveryFile) {
		t.Fatal("corrupt recovery state was accepted", err)
	}
	data, err = encodeRecovery(state, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := openRecoveryStore(dir, "primary"); !errors.Is(err, errInvalidRecoveryFile) {
		t.Fatal("nonprivate recovery state was accepted", err)
	}
}

func TestRecoveryWriteFailureLeavesAcceptedState(t *testing.T) {
	dir := privateTestDirectory(t)
	client := New(nil)
	if err := client.OpenRecovery(dir, "primary"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.CloseRecovery(t.Context()) })
	if err := os.Mkdir(filepath.Join(dir, recoveryFilename), 0700); err != nil {
		t.Fatal(err)
	}
	state := &acceptedMembership{inbounds: map[string]acceptedInbound{"full-empty": {
		layout: accountLayouts[vlessInboundType], full: true,
	}}}
	if err := client.accept(state); !errors.Is(err, membership.ErrRecoveryWrite) || client.accepted.Load() != nil {
		t.Fatal("failed publication changed accepted state", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, recoveryPending)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed publication left a candidate", err)
	}
}

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
