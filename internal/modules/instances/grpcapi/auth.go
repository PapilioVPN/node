package grpcapi

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func verifyClient(ctx context.Context) error {
	client, ok := peer.FromContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "verified client certificate required")
	}
	identity, ok := client.AuthInfo.(credentials.TLSInfo)
	if !ok || len(identity.State.VerifiedChains) == 0 {
		return status.Error(codes.Unauthenticated, "verified client certificate required")
	}
	return nil
}
