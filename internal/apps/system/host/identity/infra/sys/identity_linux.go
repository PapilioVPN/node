// Package sys reads Linux host identity through the native kernel interface.
package sys

import (
	"context"
	"fmt"

	"github.com/PapilioVPN/node/internal/apps/system/host/identity/domain"
	"golang.org/x/sys/unix"
)

type Reader struct{}

// Read performs one uname call, without files, subprocesses or cached facts.
// Cancellation is checked around the synchronous syscall; it cannot interrupt it.
func (Reader) Read(ctx context.Context) (domain.Identity, error) {
	if err := ctx.Err(); err != nil {
		return domain.Identity{}, err
	}
	var host unix.Utsname
	if err := unix.Uname(&host); err != nil {
		return domain.Identity{}, fmt.Errorf("read host identity: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.Identity{}, err
	}
	return domain.Identity{
		Hostname:        unix.ByteSliceToString(host.Nodename[:]),
		OperatingSystem: unix.ByteSliceToString(host.Sysname[:]),
		Release:         unix.ByteSliceToString(host.Release[:]),
		Version:         unix.ByteSliceToString(host.Version[:]),
		Machine:         unix.ByteSliceToString(host.Machine[:]),
	}, nil
}
