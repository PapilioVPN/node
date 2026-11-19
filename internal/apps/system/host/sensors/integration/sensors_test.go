//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	sensorsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sensors/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestSensorsOverIndependentlyEnabledTransports(t *testing.T) {
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
			assertSensorReads(t, startConfiguredNode(t, pki, config), nil)
		})
	}
}
func assertSensorReads(t *testing.T, client clients, want [][]string) {
	t.Helper()
	check := func(i int, data []byte) {
		t.Helper()
		var frame struct {
			Readings   []json.RawMessage `json:"readings"`
			ObservedAt time.Time         `json:"observed_at"`
		}
		if err := json.Unmarshal(data, &frame); err != nil || frame.ObservedAt.IsZero() || frame.Readings == nil {
			t.Fatalf("invalid observation %s %v", data, err)
		}
		if want != nil {
			for _, part := range want[i] {
				if !strings.Contains(string(data), part) {
					t.Fatalf("lost %s in %s", part, data)
				}
			}
		}
	}
	paths := []string{"temperatures", "fans", "power-supplies"}
	suffixes := []string{"temperatures", "fans", "power_supplies"}
	if client.HTTP != nil {
		for i, path := range paths {
			response, err := client.HTTP.Get(client.HTTPAddress + "/" + path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("%d %s %v", response.StatusCode, body, err)
			}
			check(i, body)
		}
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		api := sensorsv1.NewHostSensorsServiceClient(client.GRPC)
		temperatures, err := api.GetTemperatures(ctx, &sensorsv1.GetTemperaturesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		fans, err := api.GetFans(ctx, &sensorsv1.GetFansRequest{})
		if err != nil {
			t.Fatal(err)
		}
		power, err := api.GetPowerSupplies(ctx, &sensorsv1.GetPowerSuppliesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		for i, message := range []proto.Message{temperatures, fans, power} {
			data, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			check(i, data)
		}
	}
	if client.NATS != nil {
		for i, suffix := range suffixes {
			message, err := client.NATS.Request("fixture.node-a.system.host.sensors."+suffix+".v1.get", []byte("{}"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			check(i, message.Data)
		}
	}
}
