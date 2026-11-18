package sys

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sys/unix"
)

const frequencyConcurrency = 8

type FrequencyReader struct{}

func (FrequencyReader) Read(ctx context.Context) (domain.Frequencies, error) {
	return readFrequencies(ctx, "/sys/devices/system/cpu/cpufreq")
}

func readFrequencies(ctx context.Context, root string) (domain.Frequencies, error) {
	if err := ctx.Err(); err != nil {
		return domain.Frequencies{}, err
	}
	directory, err := os.Open(root)
	if errors.Is(err, os.ErrNotExist) {
		if err := ctx.Err(); err != nil {
			return domain.Frequencies{}, err
		}
		return domain.Frequencies{Policies: []domain.FrequencyPolicy{}}, nil
	}
	if err != nil {
		return domain.Frequencies{}, fmt.Errorf("open CPUFreq interface: %w", err)
	}
	entries, readErr := directory.ReadDir(cpuInfoProcessors + 1)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return domain.Frequencies{}, err
	}
	if len(entries) > cpuInfoProcessors {
		return domain.Frequencies{}, errors.New("CPUFreq interface exceeds policy capacity")
	}
	ids := []uint32{}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "policy") {
			continue
		}
		id, err := strconv.ParseUint(strings.TrimPrefix(entry.Name(), "policy"), 10, 32)
		if err != nil {
			return domain.Frequencies{}, fmt.Errorf("parse CPUFreq policy identity: %w", err)
		}
		if entry.Name() != "policy"+strconv.FormatUint(id, 10) {
			return domain.Frequencies{}, errors.New("noncanonical CPUFreq policy identity")
		}
		ids = append(ids, uint32(id))
	}
	slices.Sort(ids)
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			return domain.Frequencies{}, errors.New("duplicate CPUFreq policy identity")
		}
	}
	rows, err := readFrequencyPolicies(ctx, ids, func(ctx context.Context, id uint32) (domain.FrequencyPolicy, error) {
		return readFrequencyPolicy(ctx, filepath.Join(root, "policy"+strconv.FormatUint(uint64(id), 10)), id)
	})
	if err != nil {
		return domain.Frequencies{}, err
	}
	return domain.Frequencies{InterfacePresent: true, Policies: rows}, nil
}

// Linux has separate policy attributes rather than a bulk frequency syscall.
// Bound active native reads, retain numeric order, cancel on failure and join all
// reads before returning. Context checks do not interrupt synchronous sysfs I/O.
func readFrequencyPolicies(ctx context.Context, ids []uint32, read func(context.Context, uint32) (domain.FrequencyPolicy, error)) ([]domain.FrequencyPolicy, error) {
	group, work := errgroup.WithContext(ctx)
	group.SetLimit(frequencyConcurrency)
	rows := make([]domain.FrequencyPolicy, len(ids))
	for index, id := range ids {
		if work.Err() != nil {
			break
		}
		group.Go(func() error {
			if err := work.Err(); err != nil {
				return err
			}
			row, err := read(work, id)
			if err != nil {
				return fmt.Errorf("read CPUFreq policy %d: %w", id, err)
			}
			rows[index] = row
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func readFrequencyPolicy(ctx context.Context, root string, id uint32) (domain.FrequencyPolicy, error) {
	result := domain.FrequencyPolicy{ID: id, RelatedCPUs: []uint32{}, AffectedCPUs: []uint32{}, AvailableGovernors: []string{}, AvailableKHz: []uint64{}, Unavailable: []domain.FrequencyAttribute{}}
	// A disappearing policy is a failed observation, not a fabricated empty row.
	for _, field := range []struct {
		name   string
		target *[]uint32
	}{{"related_cpus", &result.RelatedCPUs}, {"affected_cpus", &result.AffectedCPUs}} {
		text, err := frequencyText(ctx, filepath.Join(root, field.name))
		if err != nil {
			return domain.FrequencyPolicy{}, err
		}
		seen := map[uint32]bool{}
		for _, value := range strings.Fields(text) {
			number, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				return domain.FrequencyPolicy{}, err
			}
			if seen[uint32(number)] {
				return domain.FrequencyPolicy{}, errors.New("duplicate CPUFreq CPU identity")
			}
			seen[uint32(number)] = true
			if len(seen) > cpuInfoProcessors {
				return domain.FrequencyPolicy{}, errors.New("CPUFreq CPU list exceeds capacity")
			}
			*field.target = append(*field.target, uint32(number))
		}
	}
	if len(result.RelatedCPUs) == 0 {
		return domain.FrequencyPolicy{}, errors.New("missing CPUFreq related CPUs")
	}
	for _, field := range []struct {
		name   string
		target **uint64
	}{
		{"cpuinfo_cur_freq", &result.HardwareCurrentKHz}, {"cpuinfo_avg_freq", &result.HardwareAverageKHz},
		{"cpuinfo_min_freq", &result.HardwareMinKHz}, {"cpuinfo_max_freq", &result.HardwareMaxKHz},
		{"scaling_cur_freq", &result.ScalingCurrentKHz}, {"scaling_min_freq", &result.ScalingMinKHz}, {"scaling_max_freq", &result.ScalingMaxKHz},
		{"cpuinfo_transition_latency", &result.TransitionLatencyNS},
	} {
		text, ok, err := frequencyAttribute(ctx, root, field.name, &result.Unavailable)
		if err != nil {
			return domain.FrequencyPolicy{}, err
		}
		if !ok {
			continue
		}
		value, err := strconv.ParseUint(text, 10, 64)
		if err != nil {
			return domain.FrequencyPolicy{}, fmt.Errorf("parse %s: %w", field.name, err)
		}
		*field.target = &value
	}
	for _, field := range []struct {
		name   string
		target **string
	}{{"scaling_driver", &result.Driver}, {"scaling_governor", &result.Governor}} {
		text, ok, err := frequencyAttribute(ctx, root, field.name, &result.Unavailable)
		if err != nil {
			return domain.FrequencyPolicy{}, err
		}
		if ok {
			*field.target = &text
		}
	}
	text, ok, err := frequencyAttribute(ctx, root, "scaling_available_governors", &result.Unavailable)
	if err != nil {
		return domain.FrequencyPolicy{}, err
	}
	if ok {
		result.AvailableGovernors = strings.Fields(text)
	}
	text, ok, err = frequencyAttribute(ctx, root, "scaling_available_frequencies", &result.Unavailable)
	if err != nil {
		return domain.FrequencyPolicy{}, err
	}
	if ok {
		for _, raw := range strings.Fields(text) {
			value, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return domain.FrequencyPolicy{}, err
			}
			result.AvailableKHz = append(result.AvailableKHz, value)
		}
	}
	return result, nil
}

func frequencyAttribute(ctx context.Context, root, name string, unavailable *[]domain.FrequencyAttribute) (string, bool, error) {
	text, err := frequencyText(ctx, filepath.Join(root, name))
	if err == nil {
		return text, true, nil
	}
	reason := ""
	switch {
	case errors.Is(err, os.ErrNotExist):
		reason = "not_present"
	case errors.Is(err, os.ErrPermission):
		reason = "permission_denied"
	case errors.Is(err, unix.EAGAIN):
		reason = "temporarily_unavailable"
	default:
		return "", false, err
	}
	*unavailable = append(*unavailable, domain.FrequencyAttribute{Attribute: name, Reason: reason})
	return "", false, nil
}

func frequencyText(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > 64<<10 {
		return "", errors.New("CPUFreq attribute exceeds 64 KiB")
	}
	if !utf8.Valid(data) {
		return "", errors.New("invalid CPUFreq text encoding")
	}
	return strings.TrimSpace(string(data)), nil
}
