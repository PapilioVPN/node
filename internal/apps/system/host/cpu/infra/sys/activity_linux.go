package sys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

const activityLimit = 16 << 20

type ActivityReader struct{}

// Read opens one stat stream; it does not enumerate processes, interrupts or
// per-CPU files. Cancellation surrounds synchronous I/O, not interruption of it.
func (ActivityReader) Read(ctx context.Context) (domain.Activity, error) {
	return readActivity(ctx, "/proc")
}

func readActivity(ctx context.Context, root string) (domain.Activity, error) {
	if err := ctx.Err(); err != nil {
		return domain.Activity{}, err
	}
	file, err := os.Open(filepath.Join(root, "stat"))
	if err != nil {
		return domain.Activity{}, fmt.Errorf("open CPU activity: %w", err)
	}
	result, readErr := parseActivity(ctx, file)
	if err := errors.Join(readErr, file.Close()); err != nil {
		return domain.Activity{}, fmt.Errorf("read CPU activity: %w", err)
	}
	return result, nil
}

func parseActivity(ctx context.Context, source io.Reader) (domain.Activity, error) {
	if err := ctx.Err(); err != nil {
		return domain.Activity{}, err
	}
	bounded := &io.LimitedReader{R: source, N: activityLimit + 1}
	scanner := bufio.NewScanner(bounded)
	// The interrupt vector can be much longer than a normal CPU row.
	scanner.Buffer(make([]byte, 8192), 1<<20)
	var result domain.Activity
	seen := map[string]bool{}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return domain.Activity{}, err
		}
		line := scanner.Text()
		boundary := strings.IndexAny(line, " \t")
		if boundary < 0 {
			continue
		}
		key := line[:boundary]
		var target *uint64
		switch key {
		case "ctxt":
			target = &result.ContextSwitches
		case "intr":
			target = &result.Interrupts
		case "softirq":
			target = &result.SoftInterrupts
		case "processes":
			target = &result.CreatedTasks
		case "procs_running":
			target = &result.RunnableTasks
		case "procs_blocked":
			target = &result.IOBlockedTasks
		default:
			continue
		}
		if seen[key] {
			return domain.Activity{}, errors.New("duplicate CPU activity counter")
		}
		value := strings.TrimSpace(line[boundary:])
		if key == "intr" || key == "softirq" {
			// Only the total is selected: do not split/allocate the per-IRQ vector.
			for word := range strings.FieldsSeq(value) {
				value = word
				break
			}
		}
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return domain.Activity{}, fmt.Errorf("parse CPU activity %s: %w", key, err)
		}
		*target = number
		seen[key] = true
	}
	if err := scanner.Err(); err != nil {
		return domain.Activity{}, err
	}
	if bounded.N == 0 {
		return domain.Activity{}, errors.New("CPU activity exceeds 16 MiB")
	}
	if err := ctx.Err(); err != nil {
		return domain.Activity{}, err
	}
	if len(seen) != 6 {
		return domain.Activity{}, errors.New("missing CPU activity counters")
	}
	return result, nil
}
