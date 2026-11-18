//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	devicesv1 "github.com/PapilioVPN/node/internal/apps/system/storage/devices/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"google.golang.org/protobuf/encoding/protojson"
)

type deviceReference struct {
	major, minor   uint32
	sectors        uint64
	discard, flush bool
}

func diskReferences(t *testing.T) map[string]deviceReference {
	t.Helper()
	data, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]deviceReference{}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 14 {
			t.Fatal("unsupported native diskstats reference")
		}
		major, err := strconv.ParseUint(fields[0], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		minor, err := strconv.ParseUint(fields[1], 10, 32)
		if err != nil {
			t.Fatal(err)
		}
		sectors, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		rows[fields[2]] = deviceReference{major: uint32(major), minor: uint32(minor), sectors: sectors, discard: len(fields) >= 18, flush: len(fields) >= 20}
	}
	return rows
}
func TestDeviceIOOverIndependentlyEnabledTransports(t *testing.T) {
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
			assertDeviceReads(t, startConfiguredNode(t, pki, config))
		})
	}
}
func assertDeviceReads(t *testing.T, client clients) {
	t.Helper()
	before := diskReferences(t)
	names := make([]string, 0, len(before))
	for name := range before {
		names = append(names, name)
	}
	sort.Strings(names)
	check := func(data []byte) {
		t.Helper()
		var result struct {
			Devices []struct {
				Name    string
				Major   uint32
				Minor   uint32
				Sectors uint64 `json:"read_sectors,string"`
				Discard *json.RawMessage
				Flush   *json.RawMessage
			}
			SectorSize uint32    `json:"sector_size_bytes"`
			Next       *string   `json:"next_after_name"`
			ObservedAt time.Time `json:"observed_at"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.SectorSize != 512 || result.ObservedAt.IsZero() || len(result.Devices) != min(len(names), 1) {
			t.Fatalf("invalid native device page: %s", data)
		}
		if len(names) == 0 {
			if result.Next != nil {
				t.Fatal("empty inventory has cursor")
			}
			return
		}
		got := result.Devices[0]
		expected := before[names[0]]
		after, exists := diskReferences(t)[names[0]]
		if !exists || got.Name != names[0] || got.Major != expected.major || got.Minor != expected.minor || got.Sectors < expected.sectors || got.Sectors > after.sectors || (got.Discard != nil) != expected.discard || (got.Flush != nil) != expected.flush || result.Next != nil && *result.Next != got.Name {
			t.Fatalf("lost native counters/presence: %s", data)
		}
	}
	if client.HTTP != nil {
		response, err := client.HTTP.Get(client.HTTPAddress + "?limit=1")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("HTTP %d %s %v", response.StatusCode, body, err)
		}
		check(body)
	}
	if client.GRPC != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, err := devicesv1.NewDeviceIOServiceClient(client.GRPC).ListDevices(ctx, &devicesv1.ListDevicesRequest{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		check(body)
	}
	if client.NATS != nil {
		message, err := client.NATS.Request("fixture.node-a.system.storage.devices.v1.list", []byte(`{"limit":1}`), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		check(message.Data)
	}
}
