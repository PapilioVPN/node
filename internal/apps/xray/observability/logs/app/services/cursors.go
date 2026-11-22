package services

import (
	"encoding/base64"
	"encoding/binary"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

type position struct {
	instance, execution string
	generation, offset  uint64
	skipping            bool
}

// Cursors contain selectors only, not raw fragments, credentials or server state.
// They can be replayed or used after Node reattachment while the execution and
// prefix survive. The version permits changing framing without guessing offsets.
func encode(p position) string {
	data := make([]byte, 18)
	data[0] = 1
	if p.skipping {
		data[1] = 1
	}
	binary.BigEndian.PutUint64(data[2:10], p.generation)
	binary.BigEndian.PutUint64(data[10:18], p.offset)
	data = append(data, p.instance...)
	data = append(data, 0)
	data = append(data, p.execution...)
	return base64.RawURLEncoding.EncodeToString(data)
}
func decode(cursor, id string) (position, error) {
	if len(cursor) > 512 {
		return position{}, domain.ErrInvalidArgument
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil || len(data) < 21 || data[0] != 1 || data[1] > 1 {
		return position{}, domain.ErrInvalidArgument
	}
	instance, execution, ok := strings.Cut(string(data[18:]), "\x00")
	p := position{instance: instance, execution: execution, generation: binary.BigEndian.Uint64(data[2:10]), offset: binary.BigEndian.Uint64(data[10:18]), skipping: data[1] == 1}
	if !ok || instance != id || validators.ID(execution) != nil || p.generation == 0 || p.offset > domain.MaxOutputBytes {
		return position{}, domain.ErrInvalidArgument
	}
	return p, nil
}
