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
	files, err := b.stageSources(ctx, sources)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, files.close()) }()
	ctx = files.ctx
	arguments := append([]string{"convert", "pb", "-o", "/proc/self/fd/4"}, files.arguments...)
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
	command.ExtraFiles = []*os.File{files.input, writer}
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
