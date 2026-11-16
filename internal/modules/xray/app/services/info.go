// Package services owns Xray-specific operations independently of native I/O and transports.
package services

import (
	"context"
	"errors"
	"maps"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/xray/domain"
)

// BinaryInfoReader observes a configured executable without controlling a runtime.
type BinaryInfoReader interface {
	ReadBinaryInfo(context.Context) (domain.BinaryInfo, error)
}

// BinaryInfoService selects configured Xray instances through immutable bindings.
// A nil reader denotes an existing Xray instance with inspection disabled.
type BinaryInfoService struct {
	readers map[instances.InstanceID]BinaryInfoReader
}

func NewBinaryInfoService(readers map[instances.InstanceID]BinaryInfoReader) *BinaryInfoService {
	return &BinaryInfoService{readers: maps.Clone(readers)}
}

func (s *BinaryInfoService) GetBinaryInfo(ctx context.Context, id instances.InstanceID) (domain.InstanceBinaryInfo, error) {
	if err := ctx.Err(); err != nil {
		return domain.InstanceBinaryInfo{}, err
	}
	reader, exists := s.readers[id]
	if !exists {
		return domain.InstanceBinaryInfo{}, instances.ErrNotFound
	}
	if reader == nil {
		return domain.InstanceBinaryInfo{}, domain.ErrInspectionDisabled
	}
	info, err := reader.ReadBinaryInfo(ctx)
	if cause := ctx.Err(); cause != nil {
		return domain.InstanceBinaryInfo{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InstanceBinaryInfo{}, err
	}
	return domain.InstanceBinaryInfo{InstanceID: id, Binary: info}, nil
}
