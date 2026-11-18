package sys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

const (
	cpuInfoLimit      = 64 << 20
	cpuInfoProcessors = 8192
)

type InfoReader struct{}

// Read streams one cpuinfo file. No per-CPU sysfs finishing reads or subprocess
// is required. Cancellation surrounds synchronous I/O, not interruption of it.
func (InfoReader) Read(ctx context.Context) ([]domain.Processor, error) {
	return readInfo(ctx, "/proc")
}

func readInfo(ctx context.Context, root string) ([]domain.Processor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(root, "cpuinfo"))
	if err != nil {
		return nil, fmt.Errorf("open CPU inventory: %w", err)
	}
	rows, readErr := parseInfo(ctx, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read CPU inventory: %w", err)
	}
	return rows, nil
}

func parseInfo(ctx context.Context, source io.Reader) ([]domain.Processor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bounded := &io.LimitedReader{R: source, N: cpuInfoLimit + 1}
	scanner := bufio.NewScanner(bounded)
	rows := []domain.Processor{}
	seen := make(map[uint32]bool)
	var current domain.Processor
	haveCurrent := false
	var globalModel *string
	finish := func() error {
		if !haveCurrent {
			return nil
		}
		if seen[current.ID] {
			return errors.New("duplicate CPU inventory identity")
		}
		if len(rows) >= cpuInfoProcessors {
			return errors.New("CPU inventory exceeds processor capacity")
		}
		seen[current.ID] = true
		rows = append(rows, current)
		return nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Text()
		if !utf8.ValidString(line) {
			return nil, errors.New("invalid CPU inventory text encoding")
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "processor" || key == "cpu number" {
			if err := finish(); err != nil {
				return nil, err
			}
			id, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("parse CPU identity: %w", err)
			}
			current = domain.Processor{ID: uint32(id), ModelName: globalModel, ReportedFeatures: []string{}, ReportedBugs: []string{}}
			haveCurrent = true
			continue
		}
		if key == "Processor" && !haveCurrent {
			globalModel = &value
			continue
		}
		if !haveCurrent {
			continue
		}
		if err := infoField(&current, key, value); err != nil {
			return nil, fmt.Errorf("parse CPU %d field %s: %w", current.ID, key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if bounded.N == 0 {
		return nil, errors.New("CPU inventory exceeds 64 MiB")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := finish(); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("missing CPU inventory records")
	}
	return rows, nil
}

func infoField(cpu *domain.Processor, key, value string) error {
	switch key {
	case "vendor_id", "vendorId", "CPU implementer", "mvendorid":
		cpu.VendorID = &value
	case "cpu family", "marchid":
		cpu.Family = &value
	case "model", "CPU part", "mimpid":
		cpu.Model = &value
	case "model name", "Model Name", "cpu", "uarch":
		cpu.ModelName = &value
	case "stepping", "revision", "CPU revision":
		cpu.Stepping = &value
	case "microcode":
		cpu.Microcode = &value
	case "physical id", "core id":
		id, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return err
		}
		if id < -1 {
			return errors.New("invalid CPU topology identity")
		}
		var result *int32
		if id >= 0 {
			converted := int32(id)
			result = &converted
		}
		if key == "physical id" {
			cpu.PackageID = result
		} else {
			cpu.CoreID = result
		}
	case "cpu cores", "siblings":
		count, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return err
		}
		converted := uint32(count)
		if key == "cpu cores" {
			cpu.ReportedCores = &converted
		} else {
			cpu.ReportedSiblings = &converted
		}
	case "cpu MHz", "cpu MHz dynamic", "clock":
		mhz, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(value, "MHz")), 64)
		if err != nil {
			return err
		}
		if mhz < 0 || math.IsNaN(mhz) || math.IsInf(mhz, 0) {
			return errors.New("invalid reported CPU frequency")
		}
		cpu.ReportedMHz = &mhz
	case "cache size":
		fields := strings.Fields(value)
		if len(fields) != 2 || (fields[1] != "KB" && fields[1] != "kB" && fields[1] != "KiB") {
			return errors.New("invalid CPU cache byte unit")
		}
		kib, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return err
		}
		if kib > math.MaxUint64/1024 {
			return errors.New("CPU cache byte conversion overflows")
		}
		bytes := kib * 1024
		cpu.CacheBytes = &bytes
	case "flags", "Features", "isa", "hart isa":
		cpu.ReportedFeatures = append([]string{}, strings.Fields(value)...)
	case "bugs":
		cpu.ReportedBugs = append([]string{}, strings.Fields(value)...)
	}
	return nil
}
