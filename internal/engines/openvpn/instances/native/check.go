package native

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"

	"github.com/PapilioVPN/node/internal/engines/openvpn/access"
	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

type Driver struct {
	Executable       string
	ConfigFile       string
	Revocations      *access.Store
	RuntimeDirectory string
}

func (d Driver) Check(ctx context.Context, source []byte) (lifecycle.Issue, error) {
	if err := ctx.Err(); err != nil {
		return lifecycle.Issue{}, err
	}
	if issue := checkSource(source, d.Revocations != nil); issue.Code != "" {
		return issue, nil
	}
	// The informational mode exits after the native option parser. It does
	// not validate certificate files, initialize TUN or bind a data listener.
	cmd := exec.CommandContext(ctx, d.Executable, "--config", "stdin", "--show-ciphers")
	cmd.Dir = filepath.Dir(d.ConfigFile)
	cmd.Stdin = bytes.NewReader(source)
	if err := cmd.Run(); err != nil {
		if cause := ctx.Err(); cause != nil {
			return lifecycle.Issue{}, cause
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return lifecycle.Issue{Code: "options_rejected"}, nil
		}
		return lifecycle.Issue{}, errors.Join(lifecycle.ErrCheckFailed, err)
	}
	return lifecycle.Issue{}, nil
}
