package grpcapi_test

import (
	"context"
	"time"
)

type readerFunc func(context.Context) (time.Duration, error)

func (read readerFunc) Read(ctx context.Context) (time.Duration, error) { return read(ctx) }
