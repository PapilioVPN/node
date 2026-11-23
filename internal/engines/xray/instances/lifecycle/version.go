package lifecycle

import (
	"context"
	"os/exec"
	"regexp"
	"runtime"
	"syscall"
	"time"
)

var versionLine = regexp.MustCompile(`^Xray ([0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.+-]+)?) `)

func executableVersion(ctx context.Context, executable string) string {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "version")
	var output versionOutput
	cmd.Stdout = &output
	cmd.WaitDelay = 100 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := cmd.Run(); err != nil {
		return ""
	}
	if match := versionLine.FindSubmatch(output.data); match != nil {
		return string(match[1])
	}
	return ""
}

type versionOutput struct{ data []byte }

func (o *versionOutput) Write(data []byte) (int, error) {
	o.data = append(o.data, data[:min(1024-len(o.data), len(data))]...)
	return len(data), nil
}
