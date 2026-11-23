// Package health reads ordinary Xray Observatory samples.
package health

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid Xray outbound health request")
	ErrNotFound    = errors.New("Xray instance not found")
	ErrDisabled    = errors.New("Xray ordinary Observatory is unavailable for this execution")
	ErrBusy        = errors.New("too many Xray outbound health requests")
	ErrTooLarge    = errors.New("Xray outbound observation exceeds the response limit")
	ErrUnavailable = errors.New("Xray outbound health unavailable")
)

type Status string

const (
	Healthy  Status = "healthy"
	Failed   Status = "failed"
	NoSample Status = "no_sample"
)

type Observation struct {
	Tag                 string
	Status              Status
	DelayMilliseconds   *int64
	LastSeenUnixSeconds *int64
	LastTryUnixSeconds  *int64
}

type Request struct {
	InstanceID string
	Tag        *string
}

type reader interface {
	Read(context.Context, string) ([]Observation, error)
}

type Service struct {
	reader reader
	active chan struct{}
}

func New(reader reader) *Service {
	return &Service{reader: reader, active: make(chan struct{}, 32)}
}

// Get returns the recorded snapshot or a single exact tag from it.
func (service *Service) Get(ctx context.Context, request Request) ([]Observation, error) {
	if request.InstanceID == "" || (request.Tag != nil &&
		(*request.Tag == "" || len(*request.Tag) > 4096 || !utf8.ValidString(*request.Tag))) {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case service.active <- struct{}{}:
		defer func() { <-service.active }()
	default:
		return nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	samples, err := service.reader.Read(ctx, request.InstanceID)
	if err != nil || request.Tag == nil {
		return samples, err
	}
	for i := range samples {
		if samples[i].Tag == *request.Tag {
			return samples[i : i+1 : i+1], nil
		}
	}
	return []Observation{{Tag: *request.Tag, Status: NoSample}}, nil
}
