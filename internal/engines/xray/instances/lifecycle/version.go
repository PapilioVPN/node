package lifecycle

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"syscall"
	"time"
)

var versionLine = regexp.MustCompile(`^Xray ([0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.+-]+)?) `)

func (launcher *processLauncher) readVersion(ctx context.Context, path string, identity os.FileInfo) string {
	if sameExecutable(launcher.identity, identity) {
		return launcher.version
	}
	launcher.identity, launcher.version = nil, ""
	version := executableVersion(ctx, path)
	if version != "" {
		current, err := os.Stat(path)
		if err == nil && sameExecutable(identity, current) {
			launcher.identity, launcher.version = current, version
		}
	}
	return version
}

func sameExecutable(a, b os.FileInfo) bool {
	if a == nil || b == nil || !os.SameFile(a, b) || a.Size() != b.Size() || a.Mode() != b.Mode() || !a.ModTime().Equal(b.ModTime()) {
		return false
	}
	// Linux ctime also changes on an in-place rewrite that restores mtime.
	return a.Sys().(*syscall.Stat_t).Ctim == b.Sys().(*syscall.Stat_t).Ctim
}

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
