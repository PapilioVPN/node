//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"golang.org/x/sys/unix"
)

// recoverPending closes the fork/publication gap. The private intent precedes
// fork and contains a random child-only token and a sealed output inode. The
// execution lock proves the previous owner cannot still publish another child.
// A bounded procfs scan either identifies exactly that child or fails closed.
func (p *Process) recoverPending(ctx context.Context, r executionRecord) (executionRecord, error) {
	boot, err := bootIdentity()
	if err != nil {
		return r, err
	}
	if r.BootID != boot {
		return r, p.forget(ctx, r)
	}
	if len(r.Token) != 32 || len(r.ExecutionID) != 32 {
		return r, domain.ErrOwnership
	}
	directory, err := os.Open("/proc")
	if err != nil {
		return r, err
	}
	defer directory.Close()
	found := 0
	count := 0
	for {
		entries, readErr := directory.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return r, err
			}
			pid, err := strconv.Atoi(entry.Name())
			if err != nil || pid < 1 {
				continue
			}
			count++
			if count > 65536 {
				return r, domain.ErrRecoveryRequired
			}
			var st unix.Stat_t
			if unix.Stat(fmt.Sprintf("/proc/%d", pid), &st) != nil || st.Uid != uint32(os.Geteuid()) {
				continue
			}
			if processToken(pid, r.Token) != nil {
				continue
			}
			dev, ino, err := statIdentity(fmt.Sprintf("/proc/%d/exe", pid))
			if err != nil || dev != r.BinaryDevice || ino != r.BinaryInode || found != 0 {
				return r, domain.ErrOwnership
			}
			candidate := r
			candidate.PID = pid
			output, err := openOutput(candidate)
			if err != nil {
				return r, errors.Join(domain.ErrOwnership, err)
			}
			output.Close()
			ticks, state, err := processIdentity(pid)
			if err != nil || state == "Z" {
				continue
			}
			r.PID, r.StartTicks, found = pid, ticks, pid
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return r, readErr
		}
	}
	if found == 0 {
		// No marker is not proof that fork never happened: a child may still be
		// before exec. Preserve the intent and source; never risk a second run.
		return r, domain.ErrRecoveryRequired
	}
	r.Pending = false
	return r, p.records.write(r)
}
