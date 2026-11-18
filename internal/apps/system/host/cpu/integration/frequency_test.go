//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	cpuv1 "github.com/PapilioVPN/node/internal/apps/system/host/cpu/grpcapi/proto/v1"
	"github.com/nats-io/nats.go"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const frequencySubject = "fixture.node-a.system.host.cpu.frequency.v1.get"

type frequencyPolicy struct {
	ID             uint32                               `json:"id"`
	Related        []uint32                             `json:"related_cpus"`
	Affected       []uint32                             `json:"affected_cpus"`
	Current        *uint64                              `json:"hardware_current_khz,string"`
	Average        *uint64                              `json:"hardware_average_khz,string"`
	Min            *uint64                              `json:"hardware_min_khz,string"`
	Max            *uint64                              `json:"hardware_max_khz,string"`
	ScalingCurrent *uint64                              `json:"scaling_current_khz,string"`
	ScalingMin     *uint64                              `json:"scaling_min_khz,string"`
	ScalingMax     *uint64                              `json:"scaling_max_khz,string"`
	Latency        *uint64                              `json:"transition_latency_ns,string"`
	Driver         *string                              `json:"driver"`
	Governor       *string                              `json:"governor"`
	Governors      []string                             `json:"available_governors"`
	Available      []string                             `json:"available_khz"`
	Unavailable    []struct{ Attribute, Reason string } `json:"unavailable"`
}

type frequencyInventory struct {
	Present    bool              `json:"interface_present"`
	Policies   []frequencyPolicy `json:"policies"`
	ObservedAt time.Time         `json:"observed_at"`
	DurationNS int64             `json:"duration_ns,string"`
}

func assertFrequencyReads(t *testing.T, client clients) {
	t.Helper()
	expected, present := hostFrequencies(t)
	check := func(body []byte, started time.Time) {
		t.Helper()
		var got frequencyInventory
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("frequency JSON: %s %v", body, err)
		}
		assertFrequencyInventory(t, got, present, expected, started)
	}
	if client.HTTP != nil {
		started := time.Now()
		response, err := client.HTTP.Get(strings.TrimSuffix(client.HTTPAddress, "/load") + "/cpu/frequency")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusOK || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("frequency HTTP: %s %v", body, err)
		}
		check(body, started)
	}
	if client.GRPC != nil {
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		result, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUFrequency(ctx, &cpuv1.GetHostCPUFrequencyRequest{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if result.ObservedAt.CheckValid() != nil {
			t.Fatal("invalid frequency timestamp")
		}
		body, err := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		check(body, started)
		ctx, cancel = context.WithCancel(context.Background())
		cancel()
		if _, err := cpuv1.NewHostCPUServiceClient(client.GRPC).GetHostCPUFrequency(ctx, &cpuv1.GetHostCPUFrequencyRequest{}); status.Code(err) != codes.Canceled {
			t.Fatalf("canceled frequency: %v", err)
		}
	}
	if client.NATS != nil {
		started := time.Now()
		response, err := client.NATS.Request(frequencySubject, []byte("{}"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		check(response.Data, started)
		expired := nats.NewMsg(frequencySubject)
		expired.Data = []byte("{}")
		expired.Header.Set("Papilio-Deadline", time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano))
		response, err = client.NATS.RequestMsg(expired, time.Second)
		if err != nil || string(response.Data) != `{"error":"deadline_exceeded"}` {
			t.Fatalf("expired frequency: %v %v", response, err)
		}
	}
}

func assertFrequencyInventory(t *testing.T, got frequencyInventory, present bool, expected map[uint32]map[string]frequencySource, started time.Time) {
	t.Helper()
	if got.Present != present || len(got.Policies) != len(expected) {
		t.Fatalf("frequency inventory incomplete: %+v; expected %d/%v", got, len(expected), present)
	}
	seen := map[uint32]bool{}
	var previous uint32
	for i, row := range got.Policies {
		raw, ok := expected[row.ID]
		if !ok || seen[row.ID] || i > 0 && row.ID <= previous {
			t.Fatalf("lost policy identity/order: %+v", got.Policies)
		}
		seen[row.ID] = true
		previous = row.ID
		for _, field := range []struct {
			name   string
			values []uint32
		}{{"related_cpus", row.Related}, {"affected_cpus", row.Affected}} {
			words := strings.Fields(raw[field.name].text)
			want := make([]uint32, 0, len(words))
			if raw[field.name].err != nil {
				t.Fatal(raw[field.name].err)
			}
			for _, word := range words {
				value, err := strconv.ParseUint(word, 10, 32)
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, uint32(value))
			}
			if !reflect.DeepEqual(field.values, want) && !(len(field.values) == 0 && len(want) == 0) {
				t.Fatalf("policy %d %s: %v want %v", row.ID, field.name, field.values, want)
			}
		}
		issues := map[string]string{}
		for _, issue := range row.Unavailable {
			if _, ok := issues[issue.Attribute]; ok {
				t.Fatal("duplicate attribute issue")
			}
			issues[issue.Attribute] = issue.Reason
		}
		for _, field := range []struct {
			name    string
			value   *uint64
			dynamic bool
		}{
			{"cpuinfo_cur_freq", row.Current, true}, {"cpuinfo_avg_freq", row.Average, true},
			{"cpuinfo_min_freq", row.Min, false}, {"cpuinfo_max_freq", row.Max, false},
			{"scaling_cur_freq", row.ScalingCurrent, true}, {"scaling_min_freq", row.ScalingMin, false}, {"scaling_max_freq", row.ScalingMax, false},
			{"cpuinfo_transition_latency", row.Latency, false},
		} {
			source := raw[field.name]
			reason := frequencyReason(source.err)
			if field.dynamic && (reason == "temporarily_unavailable" || issues[field.name] == "temporarily_unavailable") {
				continue
			}
			if reason != "" {
				if field.value != nil || issues[field.name] != reason {
					t.Fatalf("policy %d fabricated %s: %v/%s want %s", row.ID, field.name, field.value, issues[field.name], reason)
				}
				continue
			}
			if source.err != nil {
				t.Fatal(source.err)
			}
			if field.value == nil || issues[field.name] != "" {
				t.Fatalf("policy %d dropped available %s", row.ID, field.name)
			}
			if !field.dynamic {
				want, err := strconv.ParseUint(source.text, 10, 64)
				if err != nil || *field.value != want {
					t.Fatalf("policy %d incorrect %s: %v want %s", row.ID, field.name, field.value, source.text)
				}
			}
		}
		for _, field := range []struct {
			name  string
			value *string
		}{{"scaling_driver", row.Driver}, {"scaling_governor", row.Governor}} {
			source := raw[field.name]
			reason := frequencyReason(source.err)
			if reason != "" {
				if field.value != nil || issues[field.name] != reason {
					t.Fatal("fabricated frequency descriptor")
				}
				continue
			}
			if source.err != nil || field.value == nil || *field.value != source.text {
				t.Fatalf("incorrect descriptor %s: %v/%v", field.name, field.value, source)
			}
		}
		for _, field := range []struct {
			name   string
			values []string
		}{{"scaling_available_governors", row.Governors}, {"scaling_available_frequencies", row.Available}} {
			source := raw[field.name]
			reason := frequencyReason(source.err)
			if reason != "" {
				if len(field.values) != 0 || issues[field.name] != reason {
					t.Fatal("fabricated available frequencies/governors")
				}
				continue
			}
			if source.err != nil || strings.Join(field.values, " ") != strings.Join(strings.Fields(source.text), " ") {
				t.Fatalf("lost available values: %s", field.name)
			}
		}
	}
	if got.ObservedAt.Before(started) || got.ObservedAt.After(time.Now()) || got.DurationNS < 0 || got.DurationNS > int64(time.Since(started)) {
		t.Fatalf("invalid frequency timing: %+v", got)
	}
}

func frequencyReason(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, os.ErrNotExist):
		return "not_present"
	case errors.Is(err, os.ErrPermission):
		return "permission_denied"
	case errors.Is(err, unix.EAGAIN):
		return "temporarily_unavailable"
	default:
		return ""
	}
}
