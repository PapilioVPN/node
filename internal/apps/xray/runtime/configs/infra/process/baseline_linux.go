//go:build linux

package process

import (
	"context"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/policies"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"io"
	"os"
	"path/filepath"
)

type Baseline struct{ Path string }

// Read captures the configured original file for explicit recovery of revision
// zero. It does not attest that an externally edited file matches the live child;
// first adoption always requires restart, even for equal candidate bytes.
func (b Baseline) Read(ctx context.Context) (domain.Desired, error) {
	if err := ctx.Err(); err != nil {
		return domain.Desired{}, err
	}
	f, err := os.Open(b.Path)
	if err != nil {
		return domain.Desired{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return domain.Desired{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > domain.MaxCandidateBytes {
		return domain.Desired{}, domain.ErrTooLarge
	}
	content, err := io.ReadAll(io.LimitReader(f, domain.MaxCandidateBytes+1))
	if err != nil {
		return domain.Desired{}, err
	}
	sources := []domain.Source{{Name: filepath.Base(b.Path), Content: content}}
	if err := validators.Sources(sources); err != nil {
		return domain.Desired{}, err
	}
	return domain.Desired{Metadata: domain.DesiredMetadata{SHA256: policies.SourceDigest(sources), SourceCount: 1}, Sources: sources}, ctx.Err()
}
