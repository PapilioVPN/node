package validation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
)

// Xray v26.11.9 uses exit 23 for configuration loading and construction errors.
const rejectedExitCode = 23

// Only numeric source locations are retained from native diagnostics.
var location = regexp.MustCompile(` > infra/conf/serial: failed to read config file at line ([0-9]{1,9}) char ([0-9]{1,9}) >`)

func runNative(ctx context.Context, config Config) (issue *Issue, err error) {
	file, err := os.OpenFile(config.ConfigFile, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	if err := errors.Join(statErr, file.Close()); err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("xray configuration must be a regular file")
	}

	run, err := runconfig.Prepare(config.ConfigFile, config.RuntimeDirectory)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := run.Cleanup(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove validation runtime directory: %w", cleanupErr))
		}
	}()
	cmd := exec.CommandContext(ctx, config.Executable, append(run.Args, "-test")...)
	cmd.Stdin = bytes.NewReader(run.Overlay)
	var output diagnostics
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}

	// Pdeathsig follows the creating thread. Run holds it until the child exits.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	runErr := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if runErr == nil {
		return nil, nil
	}
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() != rejectedExitCode {
		return nil, runErr
	}

	matches := location.FindSubmatch(output.data)
	if matches == nil {
		return &Issue{Code: ConfigRejected}, nil
	}
	line, _ := strconv.ParseUint(string(matches[1]), 10, 32)
	column, _ := strconv.ParseUint(string(matches[2]), 10, 32)
	return &Issue{Code: DecodeError, Line: uint32(line), Column: uint32(column)}, nil
}

// diagnostics consumes all child output and retains at most 8 KiB.
type diagnostics struct{ data []byte }

func (output *diagnostics) Write(data []byte) (int, error) {
	room := 8<<10 - len(output.data)
	output.data = append(output.data, data[:min(room, len(data))]...)
	return len(data), nil
}
