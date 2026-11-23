package lifecycle

import (
	"context"
	"net/url"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/stats/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

func openNativeConnection(socket string) (*grpc.ClientConn, error) {
	target := url.URL{Scheme: "unix", Path: socket}
	return grpc.NewClient(target.String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  2 * time.Millisecond,
				Multiplier: 1.5,
				Jitter:     0.2,
				MaxDelay:   10 * time.Millisecond,
			},
			MinConnectTimeout: 200 * time.Millisecond,
		}),
	)
}

func waitForReadiness(ctx context.Context, conn grpc.ClientConnInterface) error {
	return conn.Invoke(ctx, native.GetSysStats, &emptypb.Empty{}, &native.SysStatsResponse{},
		grpc.StaticMethod(), grpc.WaitForReady(true), grpc.MaxCallRecvMsgSize(1024))
}
