package api

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
)

func TestReadUsesOnlySafeNativeChecksAndFixedCapabilityProjection(t *testing.T) {
	c := &connection{names: []string{services[0].symbol, services[4].symbol, "password-private-secret-service"}}
	r, err := New(c).Read(context.Background())
	if err != nil || c.calls != 1 || c.streams != 1 || !c.sent || !c.closed || r.State != "available" || *r.UptimeSeconds != 123 || len(r.Capabilities) != 5 {
		t.Fatal(r, err, c)
	}
	if r.Capabilities[0].State != "advertised" || r.Capabilities[1].State != "not_registered" || r.Capabilities[4].State != "verified" {
		t.Fatal(r)
	}
	for _, v := range r.Capabilities {
		if strings.Contains(v.Name, "private") {
			t.Fatal(v)
		}
	}
}
func TestDiscoveryUnknownDoesNotBecomeDisabled(t *testing.T) {
	c := &connection{reflectionError: status.Error(codes.Unimplemented, "password-private")}
	r, err := New(c).Read(context.Background())
	if err != nil || r.State != "available" || r.Discovery.State != "unsupported" || r.Runtime.State != "available" || r.Capabilities[0].State != "unknown" {
		t.Fatal(r, err)
	}
	c.statsError = status.Error(codes.Unavailable, "key-private")
	c.reflectionError = c.statsError
	r, err = New(c).Read(context.Background())
	if err != nil || r.State != "unavailable" || r.UptimeSeconds != nil {
		t.Fatal(r, err)
	}
	c.statsError = nil
	c.reflectionError = nil
	c.names = make([]string, 65)
	r, err = New(c).Read(context.Background())
	if err != nil || r.Discovery.Reason != "resource_exhausted" {
		t.Fatal(r, err)
	}
}

func TestMalformedReflectionErrorDoesNotInventAbsence(t *testing.T) {
	c := &connection{response: &reflection.ServerReflectionResponse{MessageResponse: &reflection.ServerReflectionResponse_ErrorResponse{ErrorResponse: &reflection.ErrorResponse{ErrorCode: 0, ErrorMessage: "password-private"}}}}
	r, err := New(c).Read(context.Background())
	if err != nil || r.Discovery.Reason != "invalid_response" || r.Capabilities[0].State != "unknown" {
		t.Fatal(r, err)
	}
}
