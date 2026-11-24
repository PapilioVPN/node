package native

//go:generate protoc -I . --go_out=. --go_opt=paths=source_relative recovery_state.proto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"syscall"
	"unicode"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/proto"
)

const (
	recoveryFilename = "accounts.state"
	recoveryPending  = "accounts.pending"
	recoveryVersion  = 1
	// Covers the native membership budget plus names, inbound tags and framing.
	maxRecoveryFileBytes = 20 << 20
)

var errInvalidRecoveryFile = errors.New("invalid Xray account recovery state")

type recoveryStore struct {
	root       *os.Root
	directory  *os.File // Holds the exclusive lock and syncs directory entries.
	instanceID string
	unsynced   bool
}

// OpenRecovery locks and loads an existing private directory during application startup.
func (c *Client) OpenRecovery(directory, instanceID string) error {
	store, state, err := openRecoveryStore(directory, instanceID)
	if err != nil {
		return err
	}
	c.recovery = store
	c.accepted.Store(state)
	return nil
}

// CloseRecovery releases the directory after the last admitted account mutation.
func (c *Client) CloseRecovery(ctx context.Context) error {
	if err := c.mutation.Acquire(ctx, 1); err != nil {
		return err
	}
	defer c.mutation.Release(1)
	if c.recovery == nil {
		return nil
	}
	return c.recovery.close()
}

func openRecoveryStore(path, instanceID string) (*recoveryStore, *acceptedMembership, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, err
	}
	directory, err := root.Open(".")
	if err != nil {
		return nil, nil, errors.Join(err, root.Close())
	}
	store := &recoveryStore{root: root, directory: directory, instanceID: instanceID}
	info, err := directory.Stat()
	if err != nil {
		return nil, nil, errors.Join(err, store.close())
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !ownedByNode(info) {
		return nil, nil, errors.Join(errors.New("Xray state directory must be owned by Node with mode 0700"), store.close())
	}
	if err := unix.Flock(int(directory.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("lock Xray state directory: %w", err), store.close())
	}
	state, err := store.load()
	if err != nil {
		return nil, nil, errors.Join(err, store.close())
	}
	// A crash before rename leaves only an unpublished candidate at this name.
	if err := store.discardCandidate(); err != nil {
		return nil, nil, errors.Join(err, store.close())
	}
	return store, state, nil
}

func (s *recoveryStore) discardCandidate() error {
	info, err := s.root.Lstat(recoveryPending)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByNode(info) {
		return errInvalidRecoveryFile
	}
	return s.root.Remove(recoveryPending)
}

func ownedByNode(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func (s *recoveryStore) close() error {
	return errors.Join(s.root.Close(), s.directory.Close())
}

func (s *recoveryStore) load() (*acceptedMembership, error) {
	file, err := s.root.OpenFile(recoveryFilename, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !ownedByNode(info) || info.Size() > maxRecoveryFileBytes {
		return nil, errInvalidRecoveryFile
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRecoveryFileBytes+1))
	if err != nil {
		return nil, err
	}
	return decodeRecovery(data, s.instanceID)
}

// accept is called under the account mutation gate after native acknowledgement.
func (c *Client) accept(next *acceptedMembership) error {
	if c.recovery != nil {
		if sameAccepted(c.accepted.Load(), next) && !c.recovery.unsynced {
			return nil
		}
		published, err := c.recovery.save(next)
		if err != nil {
			if published {
				c.accepted.Store(next)
				return errors.Join(membership.ErrRecoveryCommitUnknown, err)
			}
			return errors.Join(membership.ErrRecoveryWrite, err)
		}
	}
	c.accepted.Store(next)
	return nil
}

func sameAccepted(previous, next *acceptedMembership) bool {
	if previous == nil {
		return len(next.inbounds) == 0
	}
	return maps.EqualFunc(previous.inbounds, next.inbounds, func(a, b acceptedInbound) bool {
		return a.layout == b.layout && a.keySize == b.keySize && a.full == b.full && a.bytes == b.bytes &&
			maps.EqualFunc(a.accounts, b.accounts, func(x, y *User) bool { return x == y || proto.Equal(x, y) })
	})
}

func (s *recoveryStore) save(state *acceptedMembership) (bool, error) {
	data, err := encodeRecovery(state, s.instanceID)
	if err != nil {
		return false, err
	}
	file, err := s.root.OpenFile(recoveryPending, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false, err
	}
	defer s.root.Remove(recoveryPending)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	if err := errors.Join(err, file.Close()); err != nil {
		return false, err
	}
	if err := s.root.Rename(recoveryPending, recoveryFilename); err != nil {
		return false, err
	}
	err = s.directory.Sync()
	s.unsynced = err != nil
	return true, err
}

func encodeRecovery(state *acceptedMembership, instanceID string) ([]byte, error) {
	record := &AccountRecoveryState{Version: recoveryVersion, InstanceId: instanceID,
		Inbounds: make(map[string]*RecoveryInbound, len(state.inbounds))}
	for tag, inbound := range state.inbounds {
		entry := &RecoveryInbound{AccountType: inbound.layout.accountType, Ss2022KeySize: uint32(inbound.keySize), Full: inbound.full,
			Members: make(map[string]*RecoveryMember, len(inbound.accounts))}
		for name, user := range inbound.accounts {
			entry.Members[name] = &RecoveryMember{User: user}
		}
		record.Inbounds[tag] = entry
	}
	size := sha256.Size + proto.Size(record)
	if size > maxRecoveryFileBytes {
		return nil, membership.ErrTooLarge
	}
	data, err := (proto.MarshalOptions{}).MarshalAppend(make([]byte, sha256.Size, size), record)
	if err != nil {
		return nil, errInvalidRecoveryFile
	}
	digest := sha256.Sum256(data[sha256.Size:])
	copy(data, digest[:])
	return data, nil
}

func decodeRecovery(data []byte, instanceID string) (*acceptedMembership, error) {
	if len(data) <= sha256.Size || len(data) > maxRecoveryFileBytes {
		return nil, errInvalidRecoveryFile
	}
	digest := sha256.Sum256(data[sha256.Size:])
	if !bytes.Equal(digest[:], data[:sha256.Size]) {
		return nil, errInvalidRecoveryFile
	}
	var record AccountRecoveryState
	if proto.Unmarshal(data[sha256.Size:], &record) != nil || record.Version != recoveryVersion ||
		record.InstanceId != instanceID || len(record.Inbounds) > maxInbounds {
		return nil, errInvalidRecoveryFile
	}
	state := &acceptedMembership{inbounds: make(map[string]acceptedInbound, len(record.Inbounds))}
	var totalBytes, members int
	for tag, entry := range record.Inbounds {
		var layout accountLayout
		for _, candidate := range accountLayouts {
			if candidate.accountType == entry.GetAccountType() {
				layout = candidate
				break
			}
		}
		keySize := int(entry.GetSs2022KeySize())
		if !validRecoveryName(tag) || layout.protocol == "" ||
			(layout.protocol == membership.SS2022 && keySize != 16 && keySize != 32) ||
			(layout.protocol != membership.SS2022 && keySize != 0) {
			return nil, errInvalidRecoveryFile
		}
		members += len(entry.Members)
		if members > maxAccounts {
			return nil, errInvalidRecoveryFile
		}
		inbound := acceptedInbound{layout: layout, keySize: keySize, full: entry.Full,
			accounts: make(map[string]*User, len(entry.Members))}
		for name, member := range entry.Members {
			user := member.GetUser()
			if name == "" || len(name) > 4*1024 || strings.IndexFunc(name, unicode.IsControl) != -1 || (user == nil && entry.Full) {
				return nil, errInvalidRecoveryFile
			}
			if user != nil {
				identity, err := (&inboundAccounts{layout: layout}).name(user.Email)
				if err != nil || identity != name || !validRecoveryName(user.Email) || user.GetAccount().GetType() != layout.accountType {
					return nil, errInvalidRecoveryFile
				}
			}
			inbound.set(name, user)
		}
		totalBytes += inbound.bytes
		if totalBytes > maxAcceptedBytes {
			return nil, errInvalidRecoveryFile
		}
		state.inbounds[tag] = inbound
	}
	return state, nil
}

func validRecoveryName(name string) bool {
	return len(name) > 0 && len(name) <= 1024 && strings.IndexFunc(name, unicode.IsControl) == -1
}
