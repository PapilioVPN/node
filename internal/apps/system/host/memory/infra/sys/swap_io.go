package sys

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const vmstatLimit = 1 << 20

// readIO scans vmstat once and keeps only the two required counters.
// The native scanner bounds each line, and the source is capped
// at 1 MiB. Missing, duplicate, malformed or overflowing counters are failures.
func readIO(ctx context.Context, source io.Reader, pageBytes uint64) (uint64, uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	bounded := &io.LimitedReader{R: source, N: vmstatLimit + 1}
	scanner := bufio.NewScanner(bounded)
	var in, out uint64
	var haveIn, haveOut bool
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || (fields[0] != "pswpin" && fields[0] != "pswpout") {
			continue
		}
		if len(fields) != 2 {
			return 0, 0, errors.New("invalid swap counter record")
		}
		pages, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("parse swap counter: %w", err)
		}
		bytes, err := scaledBytes(pages, pageBytes)
		if err != nil {
			return 0, 0, err
		}
		if fields[0] == "pswpin" {
			if haveIn {
				return 0, 0, errors.New("duplicate swap-in counter")
			}
			in, haveIn = bytes, true
		} else {
			if haveOut {
				return 0, 0, errors.New("duplicate swap-out counter")
			}
			out, haveOut = bytes, true
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("scan swap counters: %w", err)
	}
	if bounded.N == 0 {
		return 0, 0, errors.New("swap counter source exceeds 1 MiB")
	}
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	if !haveIn || !haveOut {
		return 0, 0, errors.New("required swap counters are absent")
	}
	return in, out, nil
}
