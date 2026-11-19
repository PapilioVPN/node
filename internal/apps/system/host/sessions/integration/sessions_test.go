//go:build integration && linux && amd64

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	sessionsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sessions/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSessionSourcesOverIndependentlyEnabledTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	all := nodeConfig(t, pki)
	for _, transport := range []string{"http", "grpc", "nats", "all"} {
		t.Run(transport, func(t *testing.T) {
			config := all
			if transport != "all" && transport != "http" {
				config.Communications.HTTP = nil
			}
			if transport != "all" && transport != "grpc" {
				config.Communications.GRPC = nil
			}
			if transport != "all" && transport != "nats" {
				config.Communications.NATS = nil
				config.Communications.Requests.NATS = nil
			}
			if err := config.Validate(); err != nil {
				t.Fatal(err)
			}
			assertSessionReads(t, startConfiguredNode(t, pki, config))
		})
	}
}
func assertSessionReads(t *testing.T, client clients) {
	t.Helper()
	checkList := func(data []byte) {
		t.Helper()
		var result struct {
			Sessions []struct {
				ID     string
				UID    uint32
				Leader *uint32 `json:"leader_pid"`
				Idle   *bool
			}
			Extended   bool
			ObservedAt time.Time `json:"observed_at"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Sessions) != 1 || result.Sessions[0].ID != "s1" || result.Sessions[0].UID != 42 || result.Sessions[0].Leader == nil || *result.Sessions[0].Leader != 0 || result.Sessions[0].Idle == nil || *result.Sessions[0].Idle || !result.Extended || result.ObservedAt.IsZero() {
			t.Fatalf("invalid native list: %s", data)
		}
	}
	checkRead := func(data []byte) {
		t.Helper()
		var result struct {
			Session struct {
				Summary struct {
					ID  string
					UID uint32
				}
				Started uint64 `json:"started_unix_us,string"`
				Remote  *bool
				Host    *string `json:"remote_host"`
			}
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Session.Summary.ID != "s1" || result.Session.Summary.UID != 42 || result.Session.Started != 9007199254740993 || result.Session.Remote == nil || *result.Session.Remote || result.Session.Host == nil || *result.Session.Host != "" {
			t.Fatalf("invalid native details: %s", data)
		}
	}
	checkRecords := func(data []byte) {
		t.Helper()
		var result struct {
			Records []struct {
				ID       string `json:"record_id"`
				User     string
				Terminal string
				Host     string
				Started  int64 `json:"started_unix_us,string"`
			}
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Records) != 2 || result.Records[0].ID != "p1" || result.Records[0].User != "fixture-user" || result.Records[0].Terminal != "pts/1" || result.Records[0].Host != "example.test" || result.Records[0].Started != 123456789 || result.Records[0] != result.Records[1] {
			t.Fatalf("invalid native records: %s", data)
		}
	}
	if client.HTTP != nil {
		for _, operation := range []struct {
			path  string
			check func([]byte)
		}{{client.HTTPAddress + "?limit=1", checkList}, {client.HTTPAddress + "/s1", checkRead}, {strings.TrimSuffix(client.HTTPAddress, "/sessions") + "/login-records?limit=2", checkRecords}} {
			response, err := client.HTTP.Get(operation.path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("HTTP %d %s %v", response.StatusCode, body, err)
			}
			operation.check(body)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		api := sessionsv1.NewHostSessionsServiceClient(client.GRPC)
		list, err := api.ListSessions(ctx, &sessionsv1.ListSessionsRequest{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		read, err := api.GetSession(ctx, &sessionsv1.GetSessionRequest{Id: "s1"})
		if err != nil {
			t.Fatal(err)
		}
		records, err := api.ListLoginRecords(ctx, &sessionsv1.ListLoginRecordsRequest{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		options := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
		body, err := options.Marshal(list)
		if err != nil {
			t.Fatal(err)
		}
		checkList(body)
		body, err = options.Marshal(read)
		if err != nil {
			t.Fatal(err)
		}
		checkRead(body)
		body, err = options.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		checkRecords(body)
	}
	if client.NATS != nil {
		for _, operation := range []struct {
			subject, input string
			check          func([]byte)
		}{{"sessions.v1.list", `{"limit":1}`, checkList}, {"sessions.v1.get", `{"id":"s1"}`, checkRead}, {"login_records.v1.list", `{"limit":2}`, checkRecords}} {
			message, err := client.NATS.Request("fixture.node-a.system.host."+operation.subject, []byte(operation.input), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			operation.check(message.Data)
		}
	}
}
