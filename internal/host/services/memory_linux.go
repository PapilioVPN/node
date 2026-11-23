package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"unicode"

	"golang.org/x/sys/unix"
)

type MemorySnapshot struct {
	TotalBytes     uint64
	AvailableBytes *uint64
	SwapTotalBytes uint64
	SwapFreeBytes  uint64
}

func Memory(ctx context.Context) (MemorySnapshot, error) {
	return memoryFromFile(ctx, "/proc/meminfo")
}

func memoryFromFile(ctx context.Context, path string) (MemorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return MemorySnapshot{}, err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	for err == unix.EINTR && ctx.Err() == nil {
		fd, err = unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		if cause := ctx.Err(); cause != nil {
			return MemorySnapshot{}, cause
		}
		return MemorySnapshot{}, fmt.Errorf("read Linux meminfo: %w", err)
	}
	defer unix.Close(fd)

	// Procfs reports a size of zero. A typical meminfo fits in one page.
	var buffer [4096]byte
	data := buffer[:0]
	for {
		if len(data) == cap(data) {
			data = slices.Grow(data, len(buffer))
		}
		n, err := unix.Read(fd, data[len(data):cap(data)])
		if err == unix.EINTR && ctx.Err() == nil {
			continue
		}
		if err != nil {
			if cause := ctx.Err(); cause != nil {
				return MemorySnapshot{}, cause
			}
			return MemorySnapshot{}, fmt.Errorf("read Linux meminfo: %w", err)
		}
		if n == 0 {
			break
		}
		data = data[:len(data)+n]
	}
	if err := ctx.Err(); err != nil {
		return MemorySnapshot{}, err
	}
	return parseMemory(data)
}

func parseMemory(data []byte) (MemorySnapshot, error) {
	type meminfoField struct {
		bytes   uint64
		present bool
	}
	var total, available, swapTotal, swapFree meminfoField
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		data = rest
		name, value, found := bytes.Cut(line, []byte(":"))
		if !found {
			continue
		}
		var field *meminfoField
		switch string(name) {
		case "MemTotal":
			field = &total
		case "MemAvailable":
			field = &available
		case "SwapTotal":
			field = &swapTotal
		case "SwapFree":
			field = &swapFree
		default:
			continue
		}
		if field.present {
			return MemorySnapshot{}, fmt.Errorf("duplicate %s in Linux meminfo", string(name))
		}
		value = bytes.TrimSpace(value)
		separator := bytes.IndexFunc(value, unicode.IsSpace)
		if separator < 0 || string(bytes.TrimSpace(value[separator:])) != "kB" {
			return MemorySnapshot{}, fmt.Errorf("invalid %s unit in Linux meminfo", string(name))
		}
		kib, err := strconv.ParseUint(string(value[:separator]), 10, 64)
		if err != nil {
			return MemorySnapshot{}, fmt.Errorf("parse %s: %w", string(name), err)
		}
		if kib > math.MaxUint64/1024 {
			return MemorySnapshot{}, fmt.Errorf("%s overflows a byte count", string(name))
		}
		field.bytes = kib * 1024
		field.present = true
	}

	if !total.present || !swapTotal.present || !swapFree.present {
		return MemorySnapshot{}, errors.New("missing MemTotal, SwapTotal, or SwapFree in Linux meminfo")
	}
	snapshot := MemorySnapshot{
		TotalBytes:     total.bytes,
		SwapTotalBytes: swapTotal.bytes,
		SwapFreeBytes:  swapFree.bytes,
	}
	if available.present {
		snapshot.AvailableBytes = &available.bytes
	}
	return snapshot, nil
}
