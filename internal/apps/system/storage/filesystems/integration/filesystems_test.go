//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	filesystemsv1 "github.com/PapilioVPN/node/internal/apps/system/storage/filesystems/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"golang.org/x/sys/unix"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestFilesystemObservationsOverIndependentlyEnabledTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	all := nodeConfig(t, pki)
	id := mountID(t, "/proc/self/mountinfo", "/")
	path := t.TempDir()
	var expected unix.Statfs_t
	if err := unix.Statfs(path, &expected); err != nil {
		t.Fatal(err)
	}
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
			assertFilesystemReads(t, startConfiguredNode(t, pki, config), id, "/", path, false, expected)
		})
	}
}

func mountID(t *testing.T, source, path string) uint32 {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 6 && fields[4] == path {
			id, err := strconv.ParseUint(fields[0], 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			return uint32(id)
		}
	}
	t.Fatalf("reference mount absent: %s", path)
	return 0
}

func assertFilesystemReads(t *testing.T, client clients, id uint32, mountPath, path string, readOnly bool, expected unix.Statfs_t) {
	t.Helper()
	checkMount := func(data []byte) {
		t.Helper()
		var result struct {
			Mounts []struct {
				ID       uint32
				Path     string
				ReadOnly bool `json:"read_only"`
			}
			Next       *uint32   `json:"next_after_id"`
			ObservedAt time.Time `json:"observed_at"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Mounts) != 1 || result.Mounts[0].ID != id || result.Mounts[0].Path != mountPath || readOnly && !result.Mounts[0].ReadOnly || result.ObservedAt.IsZero() || result.Next != nil && *result.Next != id {
			t.Fatalf("wrong native mount: %s", data)
		}
	}
	checkUsage := func(data []byte) {
		t.Helper()
		var result struct {
			Usage struct {
				Path       string
				Type       uint64 `json:"filesystem_type,string"`
				Total      uint64 `json:"total_bytes,string"`
				Free       uint64 `json:"free_bytes,string"`
				Available  uint64 `json:"available_bytes,string"`
				Used       uint64 `json:"used_bytes,string"`
				Reserved   uint64 `json:"reserved_bytes,string"`
				Inodes     uint64 `json:"inodes_total,string"`
				InodesFree uint64 `json:"inodes_free,string"`
				InodesUsed uint64 `json:"inodes_used,string"`
			}
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		got := result.Usage
		if got.Path != path || got.Type != uint64(expected.Type) || got.Total != expected.Blocks*uint64(expected.Bsize) || got.Total != got.Used+got.Free || got.Free != got.Available+got.Reserved || got.Inodes != got.InodesFree+got.InodesUsed {
			t.Fatalf("wrong native capacity: %s", data)
		}
	}
	if client.HTTP != nil {
		for _, operation := range []struct {
			suffix string
			check  func([]byte)
		}{{fmt.Sprintf("?after_id=%d&limit=1", id-1), checkMount}, {"/usage?path=" + url.QueryEscape(path), checkUsage}} {
			response, err := client.HTTP.Get(client.HTTPAddress + operation.suffix)
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
		api := filesystemsv1.NewFilesystemsServiceClient(client.GRPC)
		mounts, err := api.ListMounts(ctx, &filesystemsv1.ListMountsRequest{AfterId: id - 1, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		usage, err := api.GetFilesystemUsage(ctx, &filesystemsv1.GetFilesystemUsageRequest{Path: path})
		if err != nil {
			t.Fatal(err)
		}
		options := protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}
		body, err := options.Marshal(mounts)
		if err != nil {
			t.Fatal(err)
		}
		checkMount(body)
		body, err = options.Marshal(usage)
		if err != nil {
			t.Fatal(err)
		}
		checkUsage(body)
	}
	if client.NATS != nil {
		input, err := json.Marshal(struct {
			Path string `json:"path"`
		}{path})
		if err != nil {
			t.Fatal(err)
		}
		for _, operation := range []struct {
			suffix string
			input  []byte
			check  func([]byte)
		}{{"v1.list", fmt.Appendf(nil, `{"after_id":%d,"limit":1}`, id-1), checkMount}, {"usage.v1.get", input, checkUsage}} {
			message, err := client.NATS.Request("fixture.node-a.system.storage.filesystems."+operation.suffix, operation.input, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			operation.check(message.Data)
		}
	}
}
