//go:build linux

package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"golang.org/x/sys/unix"
)

type sourceRecord struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}
type record struct {
	Schema     int            `json:"schema"`
	InstanceID string         `json:"instance_id"`
	Revision   uint64         `json:"revision,string"`
	SHA256     string         `json:"sha256"`
	UpdatedAt  time.Time      `json:"updated_at"`
	Sources    []sourceRecord `json:"sources"`
}

func sourceDigest(sources []domain.Source) string {
	hash := sha256.New()
	hash.Write([]byte("papilio-xray-sources-v1\x00"))
	var size [8]byte
	for _, source := range sources {
		binary.BigEndian.PutUint64(size[:], uint64(len(source.Name)))
		hash.Write(size[:])
		hash.Write([]byte(source.Name))
		binary.BigEndian.PutUint64(size[:], uint64(len(source.Content)))
		hash.Write(size[:])
		hash.Write(source.Content)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func readRecord(root *os.Root, id string) (domain.Desired, error) {
	file, err := root.OpenFile(recordName, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return domain.Desired{}, nil
	}
	if err != nil {
		return domain.Desired{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return domain.Desired{}, err
	}
	if !info.Mode().IsRegular() {
		return domain.Desired{}, domain.ErrInvalidArgument
	}
	const maxRecordBytes = 2 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return domain.Desired{}, err
	}
	if len(data) > maxRecordBytes {
		return domain.Desired{}, domain.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var row record
	if err := decoder.Decode(&row); err != nil {
		return domain.Desired{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return domain.Desired{}, domain.ErrInvalidArgument
	}
	if row.Schema != 1 || row.InstanceID != id || row.Revision == 0 || row.UpdatedAt.IsZero() {
		return domain.Desired{}, domain.ErrInvalidArgument
	}
	sources := make([]domain.Source, len(row.Sources))
	for i, source := range row.Sources {
		sources[i] = domain.Source{Name: source.Name, Content: source.Content}
	}
	if err := validators.Sources(sources); err != nil {
		return domain.Desired{}, err
	}
	if sourceDigest(sources) != row.SHA256 {
		return domain.Desired{}, domain.ErrInvalidArgument
	}
	return domain.Desired{Metadata: domain.DesiredMetadata{Revision: row.Revision, SHA256: row.SHA256, UpdatedAt: row.UpdatedAt, SourceCount: len(sources)}, Sources: sources}, nil
}

func marshalRecord(id string, desired domain.Desired) ([]byte, error) {
	row := record{Schema: 1, InstanceID: id, Revision: desired.Metadata.Revision, SHA256: desired.Metadata.SHA256, UpdatedAt: desired.Metadata.UpdatedAt, Sources: make([]sourceRecord, len(desired.Sources))}
	for i, source := range desired.Sources {
		row.Sources[i] = sourceRecord{Name: source.Name, Content: source.Content}
	}
	return json.Marshal(row)
}
