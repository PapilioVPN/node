package native

import (
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/buildinfo"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
	"golang.org/x/sys/unix"
)

func Versions() status.Versions {
	versions := status.Versions{Node: buildinfo.Version}
	var uts unix.Utsname
	if unix.Uname(&uts) == nil {
		versions.Kernel = unix.ByteSliceToString(uts.Release[:])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "wg", "--version").Output(); err == nil {
		fields := strings.Fields(string(output))
		if len(fields) >= 2 && fields[0] == "wireguard-tools" {
			versions.Tools = fields[1]
		}
	}
	return versions
}
