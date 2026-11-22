//go:build integration

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/traffic/domain"
	stats "github.com/xtls/xray-core/app/stats/command"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestResetInvalidOrOversizedAcknowledgementsRemainUnknown(t *testing.T) {
	for _, mode := range []string{"missing", "mismatch", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			client := serveNative(t, "tcp", responseServer{read: func(_ context.Context, request *stats.GetStatsRequest) (*stats.GetStatsResponse, error) {
				if !request.Reset_ {
					t.Error("reset flag absent")
				}
				response := &stats.GetStatsResponse{Stat: &stats.Stat{Name: request.Name, Value: 0}}
				switch mode {
				case "missing":
					response.Stat = nil
				case "mismatch":
					response.Stat.Name += "other"
				case "oversized":
					data := protowire.AppendTag(nil, 100, protowire.BytesType)
					response.ProtoReflect().SetUnknown(protowire.AppendBytes(data, make([]byte, 80<<10)))
				}
				return response, nil
			}})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := New(client).Reset(ctx, domain.Selection{Scope: domain.User, Name: "a", Direction: domain.Uplink})
			if !errors.Is(err, domain.ErrResetUnknown) {
				t.Fatal("invalid ACK promised no effect", err)
			}
		})
	}
}
