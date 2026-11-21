//go:build linux

// Package state owns one private, versioned Xray source record and its file lock.
package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"golang.org/x/sys/unix"
)

const recordName = "configuration.json"

// Store caches only authoritative metadata under an exclusive writer lock.
// Original source bytes are read on demand, not retained for every instance.
// External edits to this Node-owned record are not an alternate configuration API.
type Store struct {
	directory, id     string
	mu                sync.RWMutex
	root              *os.Root
	lock              *os.File
	metadata          domain.DesiredMetadata
	uncertain, closed bool
	gate              chan struct{}
	syncRoot          func(*os.Root) error
	publish           func(*os.Root, string, string) error
}

func New(directory, id string) (*Store, error) {
	if !filepath.IsAbs(directory) || id == "" {
		return nil, domain.ErrInvalidArgument
	}
	return &Store{directory: directory, id: id, gate: make(chan struct{}, 1), syncRoot: syncDirectory, publish: func(root *os.Root, from, to string) error { return root.Rename(from, to) }}, nil
}

func (s *Store) Open(ctx context.Context) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.directory)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			root.Close()
		}
	}()
	lock, err := root.OpenFile("configuration.lock", os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			lock.Close()
		}
	}()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Join(domain.ErrCapacity, err)
	}
	desired, err := readRecord(root, s.id)
	if err != nil {
		return err
	}
	if desired.Metadata.Revision != 0 {
		if err = syncRecord(root); err != nil {
			return err
		}
		if err = s.syncRoot(root); err != nil {
			return err
		}
		desired.Metadata.Durable = true
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.root != nil {
		return domain.ErrClosed
	}
	s.root, s.lock, s.metadata = root, lock, desired.Metadata
	return nil
}

func (s *Store) acquire(ctx context.Context) (*os.Root, func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case s.gate <- struct{}{}:
	default:
		return nil, nil, domain.ErrCapacity
	}
	s.mu.RLock()
	root, closed := s.root, s.closed
	s.mu.RUnlock()
	if root == nil || closed {
		<-s.gate
		return nil, nil, domain.ErrClosed
	}
	return root, func() { <-s.gate }, nil
}

func (s *Store) Metadata(ctx context.Context) (domain.DesiredMetadata, error) {
	if err := ctx.Err(); err != nil {
		return domain.DesiredMetadata{}, err
	}
	s.mu.RLock()
	metadata, uncertain, closed, opened := s.metadata, s.uncertain, s.closed, s.root != nil
	s.mu.RUnlock()
	if closed || !opened {
		return domain.DesiredMetadata{}, domain.ErrClosed
	}
	if !uncertain {
		return metadata, nil
	}
	desired, err := s.Read(ctx)
	return desired.Metadata, err
}

func (s *Store) Read(ctx context.Context) (domain.Desired, error) {
	root, release, err := s.acquire(ctx)
	if err != nil {
		return domain.Desired{}, err
	}
	defer release()
	desired, err := readRecord(root, s.id)
	if err != nil {
		return domain.Desired{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.Desired{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.uncertain && (desired.Metadata.Revision != s.metadata.Revision || desired.Metadata.SHA256 != s.metadata.SHA256) {
		return domain.Desired{}, domain.ErrConflict
	}
	if !s.uncertain {
		desired.Metadata.Durable = s.metadata.Durable
	}
	s.metadata, s.uncertain = desired.Metadata, false
	return desired, nil
}

// Confirm syncs the current record before an explicit reapplication. It also
// resolves a previously unknown publication without rewriting/replaying input.
func (s *Store) Confirm(ctx context.Context, expected uint64) (domain.DesiredMetadata, error) {
	root, release, err := s.acquire(ctx)
	if err != nil {
		return domain.DesiredMetadata{}, err
	}
	defer release()
	desired, err := readRecord(root, s.id)
	if err != nil {
		return domain.DesiredMetadata{}, err
	}
	if desired.Metadata.Revision == 0 {
		return domain.DesiredMetadata{}, domain.ErrNoDesired
	}
	if desired.Metadata.Revision != expected {
		return desired.Metadata, domain.ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return desired.Metadata, err
	}
	if err := syncRecord(root); err != nil {
		return desired.Metadata, errors.Join(domain.ErrDurabilityUnknown, err)
	}
	if err := s.syncRoot(root); err != nil {
		return desired.Metadata, errors.Join(domain.ErrDurabilityUnknown, err)
	}
	desired.Metadata.Durable = true
	s.mu.Lock()
	s.metadata, s.uncertain = desired.Metadata, false
	s.mu.Unlock()
	return desired.Metadata, nil
}

func (s *Store) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	select {
	case s.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-s.gate }()
	s.mu.Lock()
	root, lock := s.root, s.lock
	s.root, s.lock = nil, nil
	s.mu.Unlock()
	var result error
	if lock != nil {
		result = errors.Join(result, lock.Close())
	}
	if root != nil {
		result = errors.Join(result, root.Close())
	}
	return result
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func syncRecord(root *os.Root) error {
	file, err := root.OpenFile(recordName, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
