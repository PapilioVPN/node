package services

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
)

type MemorySnapshot struct {
	TotalBytes     uint64
	AvailableBytes *uint64
	SwapTotalBytes uint64
	SwapFreeBytes  uint64
}

func Memory(ctx context.Context) (MemorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return MemorySnapshot{}, err
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return MemorySnapshot{}, fmt.Errorf("read Linux meminfo: %w", err)
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
			return MemorySnapshot{}, fmt.Errorf("duplicate %s in Linux meminfo", name)
		}
		parts := bytes.Fields(value)
		if len(parts) != 2 || string(parts[1]) != "kB" {
			return MemorySnapshot{}, fmt.Errorf("invalid %s unit in Linux meminfo", name)
		}
		kib, err := strconv.ParseUint(string(parts[0]), 10, 64)
		if err != nil {
			return MemorySnapshot{}, fmt.Errorf("parse %s: %w", name, err)
		}
		if kib > math.MaxUint64/1024 {
			return MemorySnapshot{}, fmt.Errorf("%s overflows a byte count", name)
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
