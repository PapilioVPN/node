//go:build linux

package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"golang.org/x/sys/unix"
)

type sourceRecord struct {
	Name    string `json:"name"`
	Content []byte `json:"content"`
}
type record struct {
	Schema      int                 `json:"schema"`
	InstanceID  string              `json:"instance_id"`
	Revision    uint64              `json:"revision,string"`
	SHA256      string              `json:"sha256"`
	UpdatedAt   time.Time           `json:"updated_at"`
	Sources     []sourceRecord      `json:"sources"`
	Application *domain.Application `json:"application,omitempty"`
	Previous    *record             `json:"previous,omitempty"`
}

var sourceDigest = policies.SourceDigest

func readRecord(root *os.Root, id string) (domain.Desired, error) {
	_, desired, err := readDocument(root, id)
	return desired, err
}

func readDocument(root *os.Root, id string) (record, domain.Desired, error) {
	file, err := root.OpenFile(recordName, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return record{}, domain.Desired{}, nil
	}
	if err != nil {
		return record{}, domain.Desired{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return record{}, domain.Desired{}, err
	}
	if !info.Mode().IsRegular() {
		return record{}, domain.Desired{}, domain.ErrInvalidArgument
	}
	// Two original 1 MiB collections, base64 framing and one bounded receipt.
	const maxRecordBytes = 4 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxRecordBytes+1))
	if err != nil {
		return record{}, domain.Desired{}, err
	}
	if len(data) > maxRecordBytes {
		return record{}, domain.Desired{}, domain.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var row record
	if err := decoder.Decode(&row); err != nil {
		return record{}, domain.Desired{}, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record{}, domain.Desired{}, domain.ErrInvalidArgument
	}
	if (row.Schema != 1 && row.Schema != 2) || row.InstanceID != id || row.Revision == 0 || row.UpdatedAt.IsZero() {
		return record{}, domain.Desired{}, domain.ErrInvalidArgument
	}
	if (row.Schema == 1 && (row.Application != nil || row.Previous != nil)) || (row.Schema == 2 && (row.Application == nil || row.Previous == nil)) {
		return record{}, domain.Desired{}, domain.ErrInvalidArgument
	}
	if row.Application != nil && (!validIdentity(row.Application.ID) || row.Application.InstanceID != id) {
		return record{}, domain.Desired{}, domain.ErrInvalidArgument
	}
	desired, err := decodeSources(row)
	if err != nil {
		return record{}, domain.Desired{}, err
	}
	if row.Previous != nil {
		previous := row.Previous
		if row.Application == nil || previous.Schema != 1 || previous.InstanceID != id || previous.Revision >= row.Revision || previous.Application != nil || previous.Previous != nil {
			return record{}, domain.Desired{}, domain.ErrInvalidArgument
		}
		if _, err := decodeSources(*previous); err != nil {
			return record{}, domain.Desired{}, err
		}
	}
	return row, desired, nil
}

func decodeSources(row record) (domain.Desired, error) {
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
