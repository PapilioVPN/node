// Package grpcidentity verifies native transport identity for capability adapters.
package grpcidentity

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// VerifyClient requires the native transport's verified client certificate chain.
// Operation grants remain the responsibility of the owning capability adapter.
func VerifyClient(ctx context.Context) error {
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
