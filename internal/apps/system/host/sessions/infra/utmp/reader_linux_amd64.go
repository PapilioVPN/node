//go:build linux && amd64

package utmp

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"golang.org/x/sys/unix"
)

const recordBytes = 384
const maxBytes = recordBytes * 16384

// List follows the Linux amd64 utmp ABI, whose compatibility fields use signed
// 32-bit seconds/session values. Source order and duplicates are preserved.
// Reads are not an atomic snapshot of concurrent login-file writers.
func (reader Reader) List(ctx context.Context, input domain.Selection) (domain.RecordPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.RecordPage{}, err
	}
	file, err := os.OpenFile(reader.Path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return domain.RecordPage{}, errors.Join(domain.ErrPermissionDenied, err)
		}
		return domain.RecordPage{}, errors.Join(domain.ErrUnavailable, err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return domain.RecordPage{}, err
	}
	if !stat.Mode().IsRegular() {
		return domain.RecordPage{}, domain.ErrUnsupported
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return domain.RecordPage{}, errors.Join(ctx.Err(), err)
	}
	return parse(ctx, data, input)
}
func parse(ctx context.Context, data []byte, input domain.Selection) (domain.RecordPage, error) {
	if len(data) > maxBytes {
		return domain.RecordPage{}, domain.ErrResourceExhausted
	}
	if len(data)%recordBytes != 0 {
		return domain.RecordPage{}, errors.New("truncated native login record")
	}
	rows := make([]domain.LoginRecord, 0)
	for offset := 0; offset < len(data); offset += recordBytes {
		if err := ctx.Err(); err != nil {
			return domain.RecordPage{}, err
		}
		record := data[offset : offset+recordBytes]
		if binary.LittleEndian.Uint16(record[:2]) != 7 {
			continue
		} // USER_PROCESS.
		pid := int32(binary.LittleEndian.Uint32(record[4:8]))
		sec := int32(binary.LittleEndian.Uint32(record[340:344]))
		usec := int32(binary.LittleEndian.Uint32(record[344:348]))
		if pid < 0 || usec < 0 || usec >= 1000000 {
			return domain.RecordPage{}, errors.New("invalid native login record values")
		}
		row := domain.LoginRecord{PID: pid, Terminal: text(record[8:40]), RecordID: text(record[40:44]), User: text(record[44:76]), Host: text(record[76:332]), SessionID: int32(binary.LittleEndian.Uint32(record[336:340])), StartedUnixUS: int64(sec)*1000000 + int64(usec)}
		for _, value := range []string{row.Terminal, row.RecordID, row.User, row.Host} {
			if !utf8.ValidString(value) {
				return domain.RecordPage{}, errors.New("invalid native login record text")
			}
		}
		rows = append(rows, row)
	}
	start := min(int(input.Offset), len(rows))
	end := min(start+int(input.Limit), len(rows))
	page := domain.RecordPage{Records: rows[start:end]}
	if end < len(rows) {
		next := uint32(end)
		page.NextOffset = &next
	}
	return page, nil
}
func text(data []byte) string {
	if end := bytes.IndexByte(data, 0); end >= 0 {
		data = data[:end]
	}
	return string(data)
}
