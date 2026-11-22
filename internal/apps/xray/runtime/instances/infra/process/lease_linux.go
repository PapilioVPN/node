//go:build linux

package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"golang.org/x/sys/unix"
)

const leaseName = "instance-lease.json"

func (p *Process) LoadLease(ctx context.Context) (domain.LeaseRecord, error) {
	if err := ctx.Err(); err != nil {
		return domain.LeaseRecord{}, err
	}
	if p.records == nil {
		return domain.LeaseRecord{}, domain.ErrUnsupported
	}
	f, err := p.records.root.OpenFile(leaseName, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return domain.LeaseRecord{}, nil
	}
	if err != nil {
		return domain.LeaseRecord{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return domain.LeaseRecord{}, domain.ErrOwnership
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return domain.LeaseRecord{}, err
	}
	var r domain.LeaseRecord
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil {
		return r, domain.ErrOwnership
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return r, domain.ErrOwnership
	}
	return r, nil
}
func (p *Process) SaveLease(ctx context.Context, r domain.LeaseRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.records == nil {
		return domain.ErrUnsupported
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > 4096 {
		return domain.ErrInvalidArgument
	}
	return p.records.writeFile(leaseName, data)
}
