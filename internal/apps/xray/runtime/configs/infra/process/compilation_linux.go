//go:build linux

package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

// Compile passes the complete ordered source set to one native converter. The
// private directory is inherited as fd3: native merging sees only the controller's
// basenames, not an incidental "tail" substring in Node's workspace path. The
// original instance cwd and native executable/config environment retain their
// own asset lookup semantics.
func (b *Builder) Compile(ctx context.Context, sources []domain.Source) (result domain.Compilation, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if len(sources) == 0 || len(sources) > domain.MaxSources {
		return result, domain.ErrInvalidArgument
	}
	// Reject path/format encodings before staging, even for a direct adapter caller.
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		if source.Name == "" || filepath.Base(source.Name) != source.Name || strings.ContainsAny(source.Name, "\\\x00") || seen[source.Name] || core.GetFormat(source.Name) == "" {
			return result, domain.ErrInvalidArgument
		}
		seen[source.Name] = true
	}
	ctx, release, err := b.admission.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	directory, err := os.MkdirTemp(b.config.WorkDirectory, "compilation-")
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(directory)) }()
	arguments := []string{"convert", "pb", "-o", "/proc/self/fd/4"}
	for _, source := range sources {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		if err = os.WriteFile(filepath.Join(directory, source.Name), source.Content, 0600); err != nil {
			return result, err
		}
		arguments = append(arguments, "/proc/self/fd/3/"+source.Name)
	}
	input, err := os.Open(directory)
	if err != nil {
		return result, err
	}
	defer input.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return result, err
	}
	defer reader.Close()
	defer writer.Close()
	command := exec.CommandContext(ctx, b.config.Binary, arguments...)
	command.Dir = b.config.Directory
	command.Env = []string{}
	command.WaitDelay = time.Second
	command.ExtraFiles = []*os.File{input, writer}
	// ExtraFiles pipes are not joined by Cmd.WaitDelay. Closing the owned reader
	// interrupts a blocked read on cancellation; join that callback before return.
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { reader.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	if err = command.Start(); err != nil {
		return result, err
	}
	writer.Close()
	content, readErr := io.ReadAll(io.LimitReader(reader, int64(b.config.MaxOutputBytes)+1))
	tooLarge := len(content) > b.config.MaxOutputBytes
	if readErr != nil || tooLarge {
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if cause := ctx.Err(); cause != nil {
		return result, errors.Join(cause, readErr, waitErr)
	}
	if tooLarge {
		return result, errors.Join(domain.ErrTooLarge, waitErr)
	}
	if readErr != nil {
		return result, errors.Join(readErr, waitErr)
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) && exit.ExitCode() == 1 {
			return result, errors.Join(domain.ErrRejected, waitErr)
		}
		return result, waitErr
	}
	if err = proto.Unmarshal(content, new(core.Config)); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	digest := sha256.Sum256(content)
	result.Content = content
	result.SHA256 = hex.EncodeToString(digest[:])
	return result, nil
}
