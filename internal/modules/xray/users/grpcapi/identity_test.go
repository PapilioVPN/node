package grpcapi_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// Controlled peer for unit tests; bootstrap tests exercise actual mTLS.
func verifiedContext(t *testing.T) context.Context {
	t.Helper()
	return peer.NewContext(t.Context(), &peer.Peer{AuthInfo: credentials.TLSInfo{State: tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}}})
}
