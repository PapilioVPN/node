package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const email = "Example+test@example.invalid"
const credential = "303fcd60-a8d5-4552-8b71-9a76ad9bda21"
const collision = "303fcd60-a8d5-7fff-8b71-9a76ad9bda21"

func TestVLESSAccountLifecycle(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "vless fixture")
	}))
	t.Cleanup(target.Close)
	targetPort := target.Listener.Addr().(*net.TCPAddr).Port

	t.Run("HTTP", func(t *testing.T) {
		f := startFixture(t)
		count := func(want int64) {
			t.Helper()
			raw := f.request(t, http.MethodGet, accountsPath+"/count", nil, http.StatusOK)
			var result struct{ Count int64 }
			require(t, json.Unmarshal(raw, &result))
			if result.Count != want {
				t.Fatalf("count = %d, want %d", result.Count, want)
			}
		}
		add := map[string]string{"email": email, "uuid": credential}

		count(0)
		f.request(t, http.MethodPost, strings.Replace(accountsPath, "/vless/", "/trojan/", 1), add, http.StatusConflict)
		f.request(t, http.MethodPost, strings.Replace(accountsPath, "/vless/", "/missing/", 1), add, http.StatusNotFound)
		f.request(t, http.MethodPost, accountsPath, map[string]string{"email": email, "uuid": "invalid"}, http.StatusBadRequest)
		f.request(t, http.MethodPost, accountsPath, add, http.StatusNoContent)
		f.request(t, http.MethodPost, accountsPath, add, http.StatusConflict)
		f.request(t, http.MethodPost, accountsPath, map[string]string{"email": "other", "uuid": collision}, http.StatusConflict)
		count(1)
		checkTraffic(t, f.inboundPort, targetPort, true)
		remove := map[string]string{"email": strings.ToLower(email)}
		f.request(t, http.MethodDelete, accountsPath, remove, http.StatusNoContent)
		f.request(t, http.MethodDelete, accountsPath, remove, http.StatusNotFound)
		count(0)
		checkTraffic(t, f.inboundPort, targetPort, false)
	})

	t.Run("gRPC", func(t *testing.T) {
		f := startFixture(t)
		ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
		defer cancel()
		count := func(want int64) {
			t.Helper()
			got, err := f.grpc.CountVLESSAccounts(ctx, &accountsv1.CountVLESSAccountsRequest{Id: instanceID, InboundTag: "vless"})
			require(t, err)
			if got.Count != want {
				t.Fatalf("count = %d, want %d", got.Count, want)
			}
		}
		add := &accountsv1.AddVLESSAccountRequest{Id: instanceID, InboundTag: "vless", Email: email, Uuid: credential}
		count(0)
		wrong := proto.CloneOf(add)
		wrong.InboundTag = "trojan"
		_, err := f.grpc.AddVLESSAccount(ctx, wrong)
		checkRPCError(t, err, codes.FailedPrecondition)
		wrong.InboundTag = "missing"
		_, err = f.grpc.AddVLESSAccount(ctx, wrong)
		checkRPCError(t, err, codes.NotFound)
		wrong.InboundTag, wrong.Uuid = "vless", "invalid"
		_, err = f.grpc.AddVLESSAccount(ctx, wrong)
		checkRPCError(t, err, codes.InvalidArgument)
		added, err := f.grpc.AddVLESSAccount(ctx, add)
		require(t, err)
		if proto.Size(added) != 0 {
			t.Fatal("add returned account data")
		}
		_, err = f.grpc.AddVLESSAccount(ctx, add)
		checkRPCError(t, err, codes.AlreadyExists)
		other := proto.CloneOf(add)
		other.Email, other.Uuid = "other", collision
		_, err = f.grpc.AddVLESSAccount(ctx, other)
		checkRPCError(t, err, codes.AlreadyExists)
		count(1)
		checkTraffic(t, f.inboundPort, targetPort, true)
		remove := &accountsv1.RemoveVLESSAccountRequest{Id: instanceID, InboundTag: "vless", Email: strings.ToLower(email)}
		removed, err := f.grpc.RemoveVLESSAccount(ctx, remove)
		require(t, err)
		if proto.Size(removed) != 0 {
			t.Fatal("remove returned account data")
		}
		_, err = f.grpc.RemoveVLESSAccount(ctx, remove)
		checkRPCError(t, err, codes.NotFound)
		count(0)
		checkTraffic(t, f.inboundPort, targetPort, false)

		_, err = f.grpc.AddVLESSAccount(ctx, add)
		require(t, err)
		_, err = f.instance.Stop(ctx, instanceID)
		require(t, err)
		_, err = f.instance.Start(ctx, instanceID)
		require(t, err)
		count(0)
		checkTraffic(t, f.inboundPort, targetPort, false)
		_, err = f.grpc.AddVLESSAccount(ctx, add)
		require(t, err)
		checkTraffic(t, f.inboundPort, targetPort, true)
	})
}

func (f fixture) request(t *testing.T, method, path string, input any, code int) []byte {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		require(t, err)
		body = bytes.NewReader(raw)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, f.http.URL+path, body)
	require(t, err)
	resp, err := f.http.Client().Do(req)
	require(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require(t, err)
	if bytes.Contains(data, []byte(credential)) || bytes.Contains(data, []byte(email)) {
		t.Fatal("HTTP response exposed account data")
	}
	if resp.StatusCode != code || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP response = %s, want %d and no-store", resp.Status, code)
	}
	if code == http.StatusNoContent && len(data) != 0 {
		t.Fatal("mutation response was not empty")
	}
	return data
}

func checkRPCError(t *testing.T, err error, code codes.Code) {
	t.Helper()
	if status.Code(err) != code {
		t.Fatalf("gRPC code = %s, want %s", status.Code(err), code)
	}
	if message := status.Convert(err).Message(); strings.Contains(message, credential) || strings.Contains(message, email) {
		t.Fatal("gRPC response exposed account data")
	}
}

func checkTraffic(t *testing.T, inboundPort, targetPort int, accepted bool) {
	t.Helper()
	checkTrafficCredential(t, inboundPort, targetPort, credential, accepted)
}

func checkTrafficCredential(t *testing.T, inboundPort, targetPort int, uuid string, accepted bool) {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(inboundPort)), time.Second)
	require(t, err)
	defer conn.Close()
	require(t, conn.SetDeadline(time.Now().Add(2*time.Second)))

	id, valid := membership.ParseUUID(uuid)
	if !valid {
		t.Fatal("invalid fixture UUID")
	}
	wire := append([]byte{0}, id[:]...)
	wire = append(wire, 0, 1) // Empty addons and TCP command.
	wire = binary.BigEndian.AppendUint16(wire, uint16(targetPort))
	wire = append(wire, 1, 127, 0, 0, 1) // IPv4 destination.
	wire = append(wire, "GET / HTTP/1.1\r\nHost: local\r\nConnection: close\r\n\r\n"...)
	if _, err := conn.Write(wire); err != nil {
		t.Fatal("could not send fixture request")
	}
	var header [2]byte
	_, err = io.ReadFull(conn, header[:])
	if !accepted {
		var timeout net.Error
		if err == nil || (errors.As(err, &timeout) && timeout.Timeout()) {
			t.Fatal("Xray did not reject the fresh connection")
		}
		return
	}
	require(t, err)
	if header != [2]byte{0, 0} {
		t.Fatalf("VLESS response header = %v", header)
	}
	body, err := io.ReadAll(conn)
	require(t, err)
	if !bytes.Contains(body, []byte("vless fixture")) {
		t.Fatal("authenticated connection did not reach the fixture destination")
	}
}
