package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type validationReply struct {
	Valid bool   `json:"valid"`
	Issue string `json:"issue"`
}

func assertValidation(t *testing.T, f fixture, client interfacesv1.WireGuardInterfacesServiceClient, valid, invalid []byte, activeHash string) {
	t.Helper()
	for _, tc := range []struct {
		source []byte
		want   validationReply
		issue  interfacesv1.WireGuardConfigIssue
	}{
		{valid, validationReply{Valid: true}, interfacesv1.WireGuardConfigIssue_WIRE_GUARD_CONFIG_ISSUE_UNSPECIFIED},
		{invalid, validationReply{Issue: "invalid_mtu"}, interfacesv1.WireGuardConfigIssue_WIRE_GUARD_CONFIG_ISSUE_INVALID_MTU},
		{nil, validationReply{Issue: "invalid_json"}, interfacesv1.WireGuardConfigIssue_WIRE_GUARD_CONFIG_ISSUE_INVALID_JSON},
	} {
		reply := f.validateHTTP(t, interfaceName, tc.source, http.StatusOK)
		if reply != tc.want {
			t.Fatalf("validation returned %+v, want %+v", reply, tc.want)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		rpc, err := client.ValidateConfig(ctx, &interfacesv1.WireGuardValidateConfigRequest{Name: interfaceName, ConfigJson: tc.source})
		cancel()
		if err != nil || rpc.GetValid() != tc.want.Valid || rpc.GetIssue() != tc.issue {
			t.Fatalf("gRPC validation returned %v, %v", rpc, err)
		}
		if got := f.readHTTP(t); !got.Up || got.AppliedSHA256 != activeHash {
			t.Fatalf("validation changed the active execution: %+v", got)
		}
	}
	f.validateHTTP(t, "wg-other", valid, http.StatusNotFound)
	oversized := bytes.Repeat([]byte(" "), lifecycle.MaxConfigBytes+1)
	f.validateHTTP(t, interfaceName, oversized, http.StatusRequestEntityTooLarge)
	for _, tc := range []struct {
		name   string
		source []byte
		code   codes.Code
	}{
		{"wg-other", valid, codes.NotFound},
		{interfaceName, oversized, codes.ResourceExhausted},
	} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		_, err := client.ValidateConfig(ctx, &interfacesv1.WireGuardValidateConfigRequest{Name: tc.name, ConfigJson: tc.source})
		cancel()
		if status.Code(err) != tc.code {
			t.Fatalf("gRPC validation error = %v, want %v", err, tc.code)
		}
	}
	f.assertValidationTimeout(t)
}

func (f fixture) assertValidationTimeout(t *testing.T) {
	t.Helper()
	host := strings.TrimPrefix(f.address, "http://")
	conn, err := net.DialTimeout("tcp", host, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(conn, "POST %s/config-validations HTTP/1.1\r\nHost: %s\r\nContent-Length: 10\r\n\r\n{", interfaceURL, host)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("partial request returned %s", reply.Status)
	}
}

func (f fixture) validateHTTP(t testing.TB, name string, source []byte, wantStatus int) validationReply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		f.address+"/v1/wireguard/interfaces/"+name+"/config-validations", bytes.NewReader(source))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("validation HTTP status = %s, want %d", resp.Status, wantStatus)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("validation response must be non-cacheable")
	}
	if wantStatus != http.StatusOK {
		return validationReply{}
	}
	decoder := json.NewDecoder(resp.Body)
	decoder.DisallowUnknownFields()
	var result validationReply
	if err := decoder.Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
