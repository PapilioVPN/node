package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
)

type memorySnapshot struct {
	totalBytes     uint64
	availableBytes *uint64
	swapTotalBytes uint64
	swapFreeBytes  uint64
}

func readMemory(ctx context.Context) (memorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return memorySnapshot{}, err
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memorySnapshot{}, fmt.Errorf("read Linux meminfo: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return memorySnapshot{}, err
	}
	return parseMemory(data)
}

func parseMemory(data []byte) (memorySnapshot, error) {
	type meminfoField struct {
		bytes   uint64
		present bool
	}
	var total, available, swapTotal, swapFree meminfoField
	for line := range bytes.Lines(data) {
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
			return memorySnapshot{}, fmt.Errorf("duplicate %s in Linux meminfo", name)
		}
		parts := bytes.Fields(value)
		if len(parts) != 2 || string(parts[1]) != "kB" {
			return memorySnapshot{}, fmt.Errorf("invalid %s unit in Linux meminfo", name)
		}
		kib, err := strconv.ParseUint(string(parts[0]), 10, 64)
		if err != nil {
			return memorySnapshot{}, fmt.Errorf("parse %s: %w", name, err)
		}
		if kib > math.MaxUint64/1024 {
			return memorySnapshot{}, fmt.Errorf("%s overflows a byte count", name)
		}
		field.bytes = kib * 1024
		field.present = true
	}

	if !total.present || !swapTotal.present || !swapFree.present {
		return memorySnapshot{}, errors.New("missing MemTotal, SwapTotal, or SwapFree in Linux meminfo")
	}
	snapshot := memorySnapshot{
		totalBytes:     total.bytes,
		swapTotalBytes: swapTotal.bytes,
		swapFreeBytes:  swapFree.bytes,
	}
	if available.present {
		snapshot.availableBytes = &available.bytes
	}
	return snapshot, nil
}
