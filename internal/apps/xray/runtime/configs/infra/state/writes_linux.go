//go:build linux

package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"golang.org/x/sys/unix"
)

func (s *Store) Write(ctx context.Context, expected uint64, sources []domain.Source) (result domain.Persistence, err error) {
	if err := validators.Sources(sources); err != nil {
		return result, err
	}
	root, release, err := s.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	s.mu.RLock()
	metadata, uncertain := s.metadata, s.uncertain
	s.mu.RUnlock()
	if uncertain {
		return result, domain.ErrDurabilityUnknown
	}
	if metadata.Revision != expected {
		return result, domain.ErrConflict
	}
	if expected == math.MaxUint64 {
		return result, domain.ErrCapacity
	}
	desired := domain.Desired{Metadata: domain.DesiredMetadata{Revision: expected + 1, SHA256: sourceDigest(sources), UpdatedAt: time.Now().UTC(), SourceCount: len(sources)}, Sources: sources}
	result.Metadata = desired.Metadata
	data, err := marshalRecord(s.id, desired)
	if err != nil {
		return result, err
	}
	return s.publishDocument(ctx, root, desired.Metadata, data)
}

func (s *Store) publishDocument(ctx context.Context, root *os.Root, metadata domain.DesiredMetadata, data []byte) (result domain.Persistence, err error) {
	result.Metadata = metadata
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, err
	}
	name := ".configuration-" + hex.EncodeToString(random[:])
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return result, err
	}
	defer func() {
		if removeErr := root.Remove(name); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	writeErr = errors.Join(writeErr, file.Close())
	if writeErr != nil {
		return result, writeErr
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := s.publish(root, name, recordName); err != nil {
		// A remote filesystem may lose a rename acknowledgement. Do not serve
		// cached old facts until a subsequent read has observed the actual record.
		s.mu.Lock()
		s.uncertain = true
		s.mu.Unlock()
		result.PublicationUnknown = true
		return result, errors.Join(domain.ErrDurabilityUnknown, err)
	}
	result.Published = true
	s.mu.Lock()
	s.metadata = metadata
	s.mu.Unlock()
	if err := s.syncRoot(root); err != nil {
		return result, errors.Join(domain.ErrDurabilityUnknown, err)
	}
	result.Durable = true
	result.Metadata.Durable = true
	s.mu.Lock()
	s.metadata = result.Metadata
	s.mu.Unlock()
	// A known synced publication survives cancellation immediately after the ack.
	return result, nil
}
