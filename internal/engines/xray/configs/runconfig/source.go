package runconfig

import (
	"errors"
	"io"
	"os"
	"syscall"
)

const (
	MaxSourceBytes      = 1 << 20
	MaxLocalSourceBytes = 32 << 20
)

var ErrSourceTooLarge = errors.New("xray configuration exceeds the supported size")

// ReadSource snapshots a regular file without blocking on a FIFO.
func ReadSource(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("xray configuration must be a regular file")
	}
	if info.Size() > limit {
		return nil, ErrSourceTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrSourceTooLarge
	}
	return data, nil
}
