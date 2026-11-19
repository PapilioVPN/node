// Package sys observes Node through standard Go APIs and native procfs.
package sys

import (
	"context"
	"errors"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
)

type Reader struct{ initializedAt time.Time }

func New() Reader { return Reader{initializedAt: time.Now().UTC()} }
func (reader Reader) Info(ctx context.Context) (domain.Info, error) {
	if err := ctx.Err(); err != nil {
		return domain.Info{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return domain.Info{}, err
	}
	if !utf8.ValidString(executable) {
		return domain.Info{}, errors.New("invalid executable path encoding")
	}
	result := domain.Info{GoVersion: runtime.Version(), Compiler: runtime.Compiler, OperatingSystem: runtime.GOOS, Architecture: runtime.GOARCH, Executable: executable, PID: int32(os.Getpid()), ParentPID: int32(os.Getppid()), InitializedAt: reader.initializedAt}
	if build, ok := debug.ReadBuildInfo(); ok {
		result.ModulePath = build.Main.Path
		result.ModuleVersion = build.Main.Version
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				result.Revision = &setting.Value
			case "vcs.time":
				result.RevisionTime = &setting.Value
			case "vcs.modified":
				value, err := strconv.ParseBool(setting.Value)
				if err != nil {
					return domain.Info{}, err
				}
				result.Modified = &value
			}
		}
	}
	return result, ctx.Err()
}
