//go:build linux

package process

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"golang.org/x/sys/unix"
)

const recordName = "instance-execution.json"
const tokenEnvironment = "PAPILIO_XRAY_EXECUTION"

type executionRecord struct {
	Version         int
	InstanceID      string
	BootID          string
	PID             int
	StartTicks      uint64
	BinaryDevice    uint64
	BinaryInode     uint64
	Token           string
	ExecutionID     string
	Generation      uint64
	StartedAt       time.Time
	Revision        *uint64
	Pending         bool
	SourceDirectory string
	SourceDevice    uint64
	SourceInode     uint64
	OutputDevice    uint64
	OutputInode     uint64
}
type recordStore struct {
	closeOnce sync.Once
	closeErr  error
	root      *os.Root
	lock      *os.File
	value     executionRecord
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func openRecords(directory, id string) (*recordStore, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	info, statErr := root.Stat(".")
	if statErr != nil {
		root.Close()
		return nil, statErr
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) {
		root.Close()
		return nil, domain.ErrOwnership
	}
	fail := func(err error) (*recordStore, error) { root.Close(); return nil, err }
	f, err := root.OpenFile("instance-execution.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fail(err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return fail(errors.Join(domain.ErrBusy, err))
	}
	store := &recordStore{root: root, lock: f, value: executionRecord{Version: 1, InstanceID: id}}
	file, err := root.OpenFile(recordName, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		store.close()
		return nil, err
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 32<<10 {
		store.close()
		return nil, domain.ErrOwnership
	}
	data, err := io.ReadAll(io.LimitReader(file, 32<<10+1))
	if err != nil {
		store.close()
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&store.value); err != nil {
		store.close()
		return nil, errors.Join(domain.ErrOwnership, err)
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) || store.value.Version != 1 || store.value.InstanceID != id || store.value.PID < 0 || (store.value.SourceDirectory != "" && store.value.Revision == nil) || (store.value.PID > 0 && (len(store.value.Token) != 32 || len(store.value.ExecutionID) != 32 || store.value.StartTicks == 0)) {
		store.close()
		return nil, domain.ErrOwnership
	}
	return store, nil
}
func (s *recordStore) write(value executionRecord) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > 32<<10 {
		return domain.ErrOwnership
	}
	if err := s.writeFile(recordName, data); err != nil {
		return err
	}
	s.value = value
	return nil
}
func (s *recordStore) writeFile(name string, data []byte) error {
	suffix, err := randomID()
	if err != nil {
		return err
	}
	temporary := name + "." + suffix
	file, err := s.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer s.root.Remove(temporary)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if err := s.root.Rename(temporary, name); err != nil {
		return err
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (s *recordStore) close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.closeErr = errors.Join(s.root.Close(), s.lock.Close()) })
	return s.closeErr
}

func statIdentity(path string) (uint64, uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, 0, err
	}
	return uint64(st.Dev), st.Ino, nil
}
func bootIdentity() (string, error) {
	data, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	if len(data) > 64 {
		return "", domain.ErrOwnership
	}
	return strings.TrimSpace(string(data)), nil
}
func cleanupSource(ctx context.Context, work string, record executionRecord) error {
	if record.SourceDirectory == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if work == "" || filepath.Dir(record.SourceDirectory) != filepath.Clean(work) || !strings.HasPrefix(filepath.Base(record.SourceDirectory), "execution-") {
		return domain.ErrOwnership
	}
	root, err := os.OpenRoot(work)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(filepath.Base(record.SourceDirectory))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) || uint64(st.Dev) != record.SourceDevice || st.Ino != record.SourceInode {
		return domain.ErrOwnership
	}
	return root.RemoveAll(filepath.Base(record.SourceDirectory))
}
