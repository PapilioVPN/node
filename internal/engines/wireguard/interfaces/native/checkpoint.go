package native

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const (
	checkpointFile    = "wireguard.state"
	checkpointPending = "wireguard.pending"
	checkpointLimit   = 4096
)

var errInvalidCheckpoint = errors.New("invalid wireguard recovery state")

type CheckpointStore struct {
	Path      string
	root      *os.Root
	directory *os.File
	bootID    string
	namespace string
}

type storedCheckpoint struct {
	Version   uint8                `json:"version"`
	BootID    string               `json:"boot_id"`
	Namespace string               `json:"network_namespace"`
	State     lifecycle.Checkpoint `json:"state"`
}

func (store *CheckpointStore) Open(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(store.Path)
	if err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return errors.Join(err, root.Close())
	}
	store.root, store.directory = root, directory
	store.bootID, store.namespace = strings.TrimSpace(string(bootID)), namespace
	if err := store.lock(); err != nil {
		return errors.Join(err, store.Close())
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(err, store.Close())
	}
	return nil
}

func (store *CheckpointStore) lock() error {
	info, err := store.directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !checkpointOwned(info) {
		return errors.New("wireguard state directory must be owned by Node with mode 0700")
	}
	if err := unix.Flock(int(store.directory.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock wireguard state directory: %w", err)
	}
	candidate, err := store.root.Lstat(checkpointPending)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !candidate.Mode().IsRegular() || candidate.Mode().Perm() != 0600 || !checkpointOwned(candidate) {
		return errInvalidCheckpoint
	}
	return store.root.Remove(checkpointPending)
}

func checkpointOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func (store *CheckpointStore) Load() (lifecycle.Checkpoint, error) {
	file, err := store.root.OpenFile(checkpointFile, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return lifecycle.Checkpoint{}, nil
	}
	if err != nil {
		return lifecycle.Checkpoint{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return lifecycle.Checkpoint{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !checkpointOwned(info) || info.Size() > checkpointLimit {
		return lifecycle.Checkpoint{}, errInvalidCheckpoint
	}
	data, err := io.ReadAll(io.LimitReader(file, checkpointLimit+1))
	if err != nil {
		return lifecycle.Checkpoint{}, err
	}
	var record storedCheckpoint
	if len(data) > checkpointLimit || json.Unmarshal(data, &record) != nil || record.Version != 1 {
		return lifecycle.Checkpoint{}, errInvalidCheckpoint
	}
	if record.BootID != store.bootID {
		return lifecycle.Checkpoint{}, nil
	}
	if record.Namespace != store.namespace {
		return lifecycle.Checkpoint{}, errors.New("wireguard recovery state belongs to another network namespace")
	}
	if !validCheckpoint(record.State) {
		return lifecycle.Checkpoint{}, errInvalidCheckpoint
	}
	return record.State, nil
}

func validCheckpoint(state lifecycle.Checkpoint) bool {
	id, ok := strings.CutPrefix(state.Link.Alias, "papiliovpn-node:")
	if !ok || uuid.Validate(id) != nil || state.Link.Name == "" {
		return false
	}
	if state.SHA256 == "" {
		return state.Link.Index == 0 && state.MTU == 0
	}
	digest, err := hex.DecodeString(state.SHA256)
	return err == nil && len(digest) == 32 && state.SHA256 == strings.ToLower(state.SHA256) &&
		state.Link.Index > 0 && state.MTU >= 576 && state.MTU <= 9000 &&
		len(state.Fixed.Addresses) > 0 && len(state.Fixed.Addresses) <= 16
}

func (store *CheckpointStore) Save(state lifecycle.Checkpoint) error {
	file, err := store.root.OpenFile(checkpointPending, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer store.root.Remove(checkpointPending)
	record := storedCheckpoint{Version: 1, BootID: store.bootID, Namespace: store.namespace, State: state}
	if err = json.NewEncoder(file).Encode(record); err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return err
	}
	if err := store.root.Rename(checkpointPending, checkpointFile); err != nil {
		return err
	}
	return store.directory.Sync()
}

func (store *CheckpointStore) Clear() error {
	if err := store.root.Remove(checkpointFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return store.directory.Sync()
}

func (store *CheckpointStore) Close() error {
	return errors.Join(store.root.Close(), store.directory.Close())
}
