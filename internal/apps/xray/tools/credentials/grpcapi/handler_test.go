package grpcapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/domain"
	credentialsv1 "github.com/PapilioVPN/node/internal/apps/xray/tools/credentials/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNilRequestsUnknownEnumsAndSanitizedFailure(t *testing.T) {
	h := Handler{Timeout: time.Second}
	checks := []func() error{
		func() error { _, err := h.GenerateUUID(context.Background(), nil); return err },
		func() error { _, err := h.GenerateX25519(context.Background(), nil); return err },
		func() error { _, err := h.GenerateWireGuard(context.Background(), nil); return err },
		func() error { _, err := h.GenerateMLDSA65(context.Background(), nil); return err },
		func() error { _, err := h.GenerateMLKEM768(context.Background(), nil); return err },
		func() error { _, err := h.GenerateVLESSEncryption(context.Background(), nil); return err },
		func() error { _, err := h.GenerateCertificate(context.Background(), nil); return err },
		func() error { _, err := h.GetCertificateHashes(context.Background(), nil); return err },
		func() error { _, err := h.GenerateECH(context.Background(), nil); return err },
		func() error {
			_, err := h.GenerateX25519(context.Background(), &credentialsv1.GenerateX25519Request{Encoding: credentialsv1.KeyEncoding(-1)})
			return err
		},
		func() error {
			_, err := h.GenerateECH(context.Background(), &credentialsv1.GenerateECHRequest{Format: credentialsv1.ECHFormat(999)})
			return err
		},
	}
	for _, check := range checks {
		if status.Code(check()) != codes.InvalidArgument {
			t.Fatal("invalid shape reached service")
		}
	}
	for _, test := range []struct {
		cause   error
		code    codes.Code
		message string
	}{
		{domain.ErrCapacity, codes.ResourceExhausted, "resource_exhausted"},
		{domain.ErrInvalidOutput, codes.Unavailable, "invalid_native_output"},
		{domain.ErrRejected, codes.FailedPrecondition, "native_rejected"},
		{domain.ErrNotConfigured, codes.FailedPrecondition, "facility_not_configured"},
		{errors.New("private-native-material"), codes.Unavailable, "unavailable"},
	} {
		err := failure(test.cause)
		if status.Code(err) != test.code || status.Convert(err).Message() != test.message {
			t.Fatal("failure mapping")
		}
	}
}
