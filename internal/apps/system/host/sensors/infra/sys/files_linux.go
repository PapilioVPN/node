// Package sys reads the Linux sensor ABIs directly without a CLI or daemon.
package sys

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxSensors = 4096

// Linux publishes separate sysfs attributes, not a bulk sensor syscall. Every
// descriptor closes before return. Context checks do not interrupt driver I/O.
func text(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err = errors.Join(readErr, f.Close()); err != nil {
		return "", err
	}
	if err = ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > 64<<10 || !utf8.Valid(data) {
		return "", errors.New("invalid or oversized sensor attribute")
	}
	return strings.TrimSpace(string(data)), nil
}
func entries(ctx context.Context, path string) ([]os.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []os.DirEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := f.ReadDir(maxSensors + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	if len(rows) > maxSensors {
		return nil, errors.New("sensor directory exceeds supported capacity")
	}
	return rows, ctx.Err()
}
func optionalText(ctx context.Context, path string) (*string, error) {
	value, err := text(ctx, path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &value, nil
}
func optionalNumber(ctx context.Context, path string) (*int64, error) {
	value, err := optionalText(ctx, path)
	if err != nil || value == nil {
		return nil, err
	}
	number, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return nil, err
	}
	return &number, nil
}

type channel struct{ id, device, base string }

func channels(ctx context.Context, root, prefix string) ([]channel, error) {
	chips, err := entries(ctx, filepath.Join(root, "class/hwmon"))
	if err != nil {
		return nil, err
	}
	rows := []channel{}
	for _, chip := range chips {
		if !strings.HasPrefix(chip.Name(), "hwmon") {
			continue
		}
		chipPath := filepath.Join(root, "class/hwmon", chip.Name())
		files, err := entries(ctx, chipPath)
		if err != nil {
			return nil, err
		}
		bases := channelNames(files, prefix)
		// Some older drivers place attributes in the physical device directory.
		if len(bases) == 0 {
			legacy := filepath.Join(chipPath, "device")
			files, err = entries(ctx, legacy)
			if err != nil {
				return nil, err
			}
			bases = channelNames(files, prefix)
			if len(bases) > 0 {
				chipPath = legacy
			}
		}
		if len(bases) == 0 {
			continue
		}
		device, err := text(ctx, filepath.Join(chipPath, "name"))
		if err != nil {
			return nil, err
		}
		if len(rows)+len(bases) > maxSensors {
			return nil, errors.New("sensor count exceeds supported capacity")
		}
		for _, base := range bases {
			rows = append(rows, channel{id: chip.Name() + "/" + base, device: device, base: filepath.Join(chipPath, base)})
		}
	}
	return rows, nil
}
func channelNames(files []os.DirEntry, prefix string) []string {
	bases := []string{}
	for _, file := range files {
		name := file.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, "_input") {
			continue
		}
		index := strings.TrimSuffix(strings.TrimPrefix(name, prefix), "_input")
		n, err := strconv.ParseUint(index, 10, 32)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != index {
			continue
		}
		bases = append(bases, strings.TrimSuffix(name, "_input"))
	}
	return bases
}
