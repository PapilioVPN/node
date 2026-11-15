package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/system/domain"
	rpc "github.com/PapilioVPN/node/internal/modules/system/grpcapi"
	systemv1 "github.com/PapilioVPN/node/internal/modules/system/grpcapi/proto/v1"
)

type infoReader func(context.Context) (domain.NodeInfo, error)

func (f infoReader) GetNodeInfo(ctx context.Context) (domain.NodeInfo, error) { return f(ctx) }

func TestHandlerErrorMapping(t *testing.T) {
	for _, item := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{"canceled", context.Canceled, codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"wrapped cancellation", fmt.Errorf("private native path: %w", context.Canceled), codes.Canceled},
		{"wrapped deadline", fmt.Errorf("private native path: %w", context.DeadlineExceeded), codes.DeadlineExceeded},
		{"internal", errors.New("private implementation detail"), codes.Internal},
	} {
		t.Run(item.name, func(t *testing.T) {
			handler := rpc.Handler{Info: infoReader(func(context.Context) (domain.NodeInfo, error) { return domain.NodeInfo{}, item.err })}
			_, err := handler.GetNodeInfo(context.Background(), &systemv1.GetNodeInfoRequest{})
			if status.Code(err) != item.code || strings.Contains(err.Error(), "private") {
				t.Fatalf("incorrect error mapping: %v", err)
			}
		})
	}
}
