package grpcapi

import (
	"context"
	"errors"
	"strings"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/tools/diagnostics/domain"
)

type statusReader struct{}

func (statusReader) Status(context.Context, string) (instances.Status, error) {
	return instances.Status{Execution: instances.Execution{State: instances.Exited, Generation: 9007199254740993, Failure: errors.New("private-password-key /private/config")}, ObservedAt: time.Now().UTC()}, nil
}

type largeBinary struct{}

func (largeBinary) Read(context.Context) (domain.Binary, error) {
	return domain.Binary{Version: strings.Repeat("x", domain.MaxReportBytes)}, nil
}
