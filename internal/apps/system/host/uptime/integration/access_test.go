//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	identityv1 "github.com/PapilioVPN/node/internal/apps/system/host/identity/grpcapi/proto/v1"
	uptimev1 "github.com/PapilioVPN/node/internal/apps/system/host/uptime/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestControllerCanReadBothCapabilitiesWithoutGrantConfiguration(t *testing.T) {
	client := startNode(t, "all")
	assertFreshReads(t, client)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := identityv1.NewHostIdentityServiceClient(client.GRPC).GetHostIdentity(ctx, &identityv1.GetHostIdentityRequest{}); err != nil {
		t.Fatal(err)
	}
	response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "uptime") + "identity")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	message, err := client.NATS.Request("fixture.node-a.system.host.identity.v1.get", []byte("{}"), time.Second)
	if err != nil || !strings.Contains(string(message.Data), `"identity"`) {
		t.Fatalf("identity observation: %v %v", message, err)
	}
}

func TestUptimeRejectsUnexpectedInputAndHonorsDeadline(t *testing.T) {
	client := startNode(t, "all")
	for _, request := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "?extra=1", "", 400}, {"GET", "", "{}", 400}, {"POST", "", "", 405},
	} {
		input, err := http.NewRequest(request.method, client.HTTPAddress+request.path, strings.NewReader(request.body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.HTTP.Do(input)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != request.status {
			t.Fatalf("unexpected HTTP input accepted: %d", response.StatusCode)
		}
	}
	message := nats.NewMsg(subject)
	message.Data = []byte("{}")
	message.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
	reply, err := client.NATS.RequestMsg(message, time.Second)
	if err != nil || string(reply.Data) != `{"error":"deadline_exceeded"}` {
		t.Fatalf("expired deadline: %v %v", reply, err)
	}
	reply, err = client.NATS.Request(subject, []byte(`{"unknown":1}`), time.Second)
	if err != nil || string(reply.Data) != `{"error":"invalid_argument"}` {
		t.Fatalf("unexpected NATS input: %v %v", reply, err)
	}
	_, err = client.NATS.Request("fixture.node-a.system.host.unknown.v1.get", []byte("{}"), time.Second)
	if !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("unknown contract answered: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uptimev1.NewHostUptimeServiceClient(client.GRPC).GetHostUptime(ctx, &uptimev1.GetHostUptimeRequest{})
	if status.Code(err) != codes.Canceled {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}
