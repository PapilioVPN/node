//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instancedomain "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
)

type SourceReader interface {
	Read(context.Context) (domain.Desired, error)
}

// Resolver leases private original files to the instance execution owner. The
// lease survives its request and is released only after the child has exited.
type Resolver struct {
	Reader        SourceReader
	WorkDirectory string
}

func (r Resolver) Open(ctx context.Context) (*instanceprocess.Input, error) {
	desired, err := r.Reader.Read(ctx)
	if err != nil {
		if errors.Is(err, domain.ErrCapacity) {
			err = errors.Join(instancedomain.ErrBusy, err)
		}
		if errors.Is(err, domain.ErrClosed) {
			err = errors.Join(instancedomain.ErrClosed, err)
		}
		return nil, err
	}
	if desired.Metadata.Revision == 0 {
		return nil, nil
	}
	if !desired.Metadata.Durable {
		return nil, domain.ErrDurabilityUnknown
	}
	directory, err := os.MkdirTemp(r.WorkDirectory, "execution-")
	if err != nil {
		return nil, err
	}
	input := &instanceprocess.Input{Revision: desired.Metadata.Revision, Release: func() error { return os.RemoveAll(directory) }}
	for _, source := range desired.Sources {
		if err := ctx.Err(); err != nil {
			return input, err
		}
		if err := os.WriteFile(filepath.Join(directory, source.Name), source.Content, 0600); err != nil {
			return input, err
		}
		input.Names = append(input.Names, source.Name)
	}
	input.Directory, err = os.Open(directory)
	return input, err
}
