package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// A controlled peer is unit evidence only; bootstrap tests perform native mTLS.
func verifiedContext(t *testing.T) context.Context {
	t.Helper()
	return peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{
		State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}},
	}})
}
