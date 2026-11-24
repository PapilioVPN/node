package validation

import (
	"bytes"
	"context"
	"encoding/json"
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

func runNative(ctx context.Context, config Config) (*Issue, error) {
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
	return check(ctx, config.Executable, run)
}

// CheckSource checks a proposed configuration without opening its log destinations.
func CheckSource(ctx context.Context, executable, runtimeDirectory string, source []byte) (*Issue, error) {
	return checkSource(ctx, executable, runtimeDirectory, source, true)
}

// CheckStartSource keeps Start's preflight check of the configured log destinations.
func CheckStartSource(ctx context.Context, executable, runtimeDirectory string, source []byte) (*Issue, error) {
	return checkSource(ctx, executable, runtimeDirectory, source, false)
}

func checkSource(ctx context.Context, executable, runtimeDirectory string, source []byte, quietLogs bool) (*Issue, error) {
	run, err := runconfig.PrepareSource(source, runtimeDirectory)
	if err != nil {
		return nil, err
	}
	if quietLogs {
		if err := suppressLogDestinations(&run, source); err != nil {
			return nil, errors.Join(err, run.Cleanup())
		}
	}
	return check(ctx, executable, run)
}

// Xray -test constructs its logger before exiting. Disable its writes while
// retaining the address-mask setting, which the constructor also validates.
func suppressLogDestinations(run *runconfig.Run, source []byte) error {
	var candidate struct {
		Log *struct {
			MaskAddress string `json:"maskAddress"`
		} `json:"log"`
	}
	if err := json.Unmarshal(source, &candidate); err != nil || candidate.Log == nil {
		return nil // Xray still validates the original candidate.
	}
	var overlay map[string]json.RawMessage
	if err := json.Unmarshal(run.Overlay, &overlay); err != nil {
		return err
	}
	log, err := json.Marshal(struct {
		Level       string `json:"loglevel"`
		MaskAddress string `json:"maskAddress"`
	}{Level: "none", MaskAddress: candidate.Log.MaskAddress})
	if err != nil {
		return err
	}
	overlay["log"] = log
	run.Overlay, err = json.Marshal(overlay)
	return err
}

func check(ctx context.Context, executable string, run runconfig.Run) (issue *Issue, err error) {
	defer func() {
		if cleanupErr := run.Cleanup(); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove validation runtime directory: %w", cleanupErr))
		}
	}()
	cmd := exec.CommandContext(ctx, executable, append(run.Args, "-test")...)
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
