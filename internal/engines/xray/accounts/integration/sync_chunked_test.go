package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

const streamPath = accountsPath + "/membership/stream"

func TestSyncUsersChunkedHTTPRejectsIncompleteStream(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: email, ID: credential})
	body := strings.NewReader("{\"index\":0,\"accounts\":[{\"email\":\"" + email + "\"}]}\n")
	f.streamRequest(t, body, http.StatusBadRequest)
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, trafficTarget(t), true)
}

func (f fixture) streamRequest(t *testing.T, body io.Reader, want int) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, f.http.URL+streamPath, body)
	require(t, err)
	req.Header.Set("Content-Type", "application/x-ndjson")
	resp, err := f.http.Client().Do(req)
	require(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require(t, err)
	if resp.StatusCode != want || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HTTP stream status %d, want %d and no-store", resp.StatusCode, want)
	}
	for _, secret := range []string{email, credential, rotatedCredential} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("HTTP stream response exposed account data")
		}
	}
	return raw
}

const streamRetainedCredential = "5cfd13e4-f580-4abe-9f0f-0b206cb44be3"

func TestSyncUsersChunkedHTTP(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: email, ID: credential},
		configuredAccount{Email: "retained", ID: streamRetainedCredential})
	target := trafficTarget(t)
	remove := "{\"index\":0,\"accounts\":[{\"email\":\"" + email + "\"}]}\n"
	add := "{\"index\":1,\"last\":true,\"accounts\":[{\"email\":\"new\",\"uuid\":\"" + rotatedCredential + "\"}]}\n"

	f.streamRequest(t, strings.NewReader(remove+strings.Replace(add, "\"index\":1", "\"index\":2", 1)), http.StatusBadRequest)
	f.streamRequest(t, strings.NewReader(remove+add+add), http.StatusBadRequest)
	f.streamRequest(t, strings.NewReader(remove+strings.Replace(add, "\"email\":\"new\"", "\"email\":\""+email+"\"", 1)), http.StatusBadRequest)
	f.checkCount(t, 2)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, f.http.URL+streamPath, reader)
	require(t, err)
	done := make(chan error, 1)
	go func() {
		resp, err := f.http.Client().Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		done <- err
	}()
	_, err = io.WriteString(writer, remove+add)
	require(t, err)
	f.checkCount(t, 2)
	require(t, writer.CloseWithError(errors.New("fixture body lost before EOF")))
	if err := <-done; err == nil {
		t.Fatal("interrupted upload reported successful delivery")
	}
	f.checkCount(t, 2)
	checkTraffic(t, f.inboundPort, target, true)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, false)

	response := f.streamRequest(t, strings.NewReader(remove+add), http.StatusOK)
	var result membership.SyncResult
	require(t, json.Unmarshal(response, &result))
	if result.Outcome != membership.Confirmed || result.Added != 1 || result.Removed != 1 {
		t.Fatalf("unexpected stream outcome: %+v", result)
	}
	f.checkCount(t, 2)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
	checkTrafficCredential(t, f.inboundPort, target, streamRetainedCredential, true)
}

func TestSyncUsersChunkedGRPC(t *testing.T) {
	f := startFixture(t, configuredAccount{Email: email, ID: credential},
		configuredAccount{Email: "retained", ID: streamRetainedCredential})
	target := trafficTarget(t)
	remove := &accountsv1.UsersChunk{Id: instanceID, InboundTag: "vless",
		Accounts: []*accountsv1.VLESSAccount{{Email: email}}}
	add := &accountsv1.UsersChunk{Id: instanceID, InboundTag: "vless", Index: 1, Last: true,
		Accounts: []*accountsv1.VLESSAccount{{Email: "new", Uuid: ptr(rotatedCredential)}}}
	stream, err := f.grpc.SyncUsersChunked(t.Context())
	require(t, err)
	_, err = stream.CloseAndRecv()
	checkRPCError(t, err, codes.InvalidArgument)

	deadline, stop := context.WithTimeout(t.Context(), 10*time.Second)
	defer stop()
	stream, err = f.grpc.SyncUsersChunked(deadline)
	require(t, err)
	require(t, stream.Send(remove))
	_, err = stream.CloseAndRecv()
	checkRPCError(t, err, codes.InvalidArgument)

	stream, err = f.grpc.SyncUsersChunked(deadline)
	require(t, err)
	require(t, stream.Send(remove))
	wrongScope := proto.CloneOf(add)
	wrongScope.InboundTag = "other"
	require(t, stream.Send(wrongScope))
	_, err = stream.CloseAndRecv()
	checkRPCError(t, err, codes.InvalidArgument)

	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	stream, err = f.grpc.SyncUsersChunked(ctx)
	require(t, err)
	require(t, stream.Send(remove))
	require(t, stream.Send(add))
	f.checkCount(t, 2)
	cancel()
	err = stream.RecvMsg(new(accountsv1.SyncResult))
	checkRPCError(t, err, codes.Canceled)
	f.checkCount(t, 2)
	checkTraffic(t, f.inboundPort, target, true)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, false)

	stream, err = f.grpc.SyncUsersChunked(deadline)
	require(t, err)
	require(t, stream.Send(remove))
	require(t, stream.Send(add))
	result, err := stream.CloseAndRecv()
	require(t, err)
	checkSyncResult(t, result, 1, 1)
	f.checkCount(t, 2)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
	checkTrafficCredential(t, f.inboundPort, target, streamRetainedCredential, true)
}

func TestSyncUsersChunkedAcknowledgementLost(t *testing.T) {
	f := startFixture(t)
	target := trafficTarget(t)
	service := f.interceptEdits(t, func(count int32) error {
		if count == 3 {
			return errors.New(rotatedCredential)
		}
		return nil
	})
	scope := membership.Scope{InstanceID: instanceID, InboundTag: "vless"}
	require(t, service.Add(t.Context(), membership.AddRequest{Scope: scope, Email: email, UUID: credential}))
	chunks := []membership.UsersChunk{
		{Scope: scope, Accounts: []membership.AccountInput{{Email: email}}},
		{Scope: scope, Index: 1, Last: true, Accounts: []membership.AccountInput{{Email: "new", UUID: ptr(rotatedCredential)}}},
	}
	result, err := service.SyncUsersChunked(t.Context(), func() (membership.UsersChunk, error) {
		if len(chunks) == 0 {
			return membership.UsersChunk{}, io.EOF
		}
		chunk := chunks[0]
		chunks = chunks[1:]
		return chunk, nil
	})
	require(t, err)
	if result.Outcome != membership.Unknown || result.Added != 0 || result.Removed != 1 ||
		result.Failure != "native_acknowledgement_unknown" {
		t.Fatal("lost acknowledgement did not preserve confirmed effects")
	}
	f.checkCount(t, 1)
	checkTraffic(t, f.inboundPort, target, false)
	checkTrafficCredential(t, f.inboundPort, target, rotatedCredential, true)
}
