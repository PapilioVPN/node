package lifecycle

import (
	"context"
	"net/url"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
)

const systemStatsRPC = "/xray.app.stats.command.StatsService/GetSysStats"

func waitForReadiness(ctx context.Context, socket string) error {
	target := url.URL{Scheme: "unix", Path: socket}
	conn, err := grpc.NewClient(target.String(),
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
	if err != nil {
		return err
	}
	defer conn.Close()
	// The native request is an empty message. The response fields are unused.
	return conn.Invoke(ctx, systemStatsRPC, &emptypb.Empty{}, &emptypb.Empty{},
		grpc.StaticMethod(), grpc.WaitForReady(true), grpc.MaxCallRecvMsgSize(1024))
}
