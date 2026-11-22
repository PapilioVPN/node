// Package installation provisions the initial release from local distribution files.
package installation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/assets/infra/store"
)

func Initialize(ctx context.Context, id, directory, source string) (err error) {
	repo, err := store.Open(directory, id)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, repo.Close()) }()
	state, err := repo.State(ctx)
	if err != nil {
		return err
	}
	if state.Revision != 0 {
		return domain.ErrConflict
	}
	var candidate domain.Candidate
	for _, file := range []struct {
		name    string
		limit   int
		content *domain.File
	}{
		{"xray", domain.MaxBinary, &candidate.Binary},
		{"geoip.dat", domain.MaxGeodata, &candidate.GeoIP},
		{"geosite.dat", domain.MaxGeodata, &candidate.GeoSite},
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		value, err := readArtifact(filepath.Join(source, file.name), file.limit)
		if err != nil {
			return err
		}
		*file.content = value
	}
	release, err := repo.Stage(ctx, candidate)
	if err != nil {
		return err
	}
	return repo.Initialize(ctx, release.ID)
}

func readArtifact(path string, limit int) (domain.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return domain.File{}, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > int64(limit) {
		return domain.File{}, domain.ErrInvalidArgument
	}
	file, err := os.Open(path)
	if err != nil {
		return domain.File{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return domain.File{}, err
	}
	if len(data) < 1 || len(data) > limit {
		return domain.File{}, domain.ErrInvalidArgument
	}
	digest := sha256.Sum256(data)
	return domain.File{Content: data, SHA256: hex.EncodeToString(digest[:])}, nil
}
