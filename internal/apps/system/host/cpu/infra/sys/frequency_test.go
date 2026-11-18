//go:build linux

package sys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
)

func TestFrequencyReadsSharedPoliciesOnceInNumericOrder(t *testing.T) {
	root := frequencyFixture(t, map[string]map[string]string{
		"policy10": {"related_cpus": "10 11", "affected_cpus": "10", "scaling_cur_freq": "9007199254740993", "cpuinfo_min_freq": "0", "cpuinfo_max_freq": "18446744073709551615", "scaling_driver": "driver", "scaling_governor": "governor", "scaling_available_governors": "performance powersave", "scaling_available_frequencies": "900000 1800000"},
		"policy2":  {"related_cpus": "2 3", "affected_cpus": ""},
	})
	got, err := readFrequencies(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !got.InterfacePresent || len(got.Policies) != 2 || got.Policies[0].ID != 2 || got.Policies[1].ID != 10 {
		t.Fatalf("lost policy identity/order: %+v", got)
	}
	row := got.Policies[1]
	if !reflect.DeepEqual(row.RelatedCPUs, []uint32{10, 11}) || !reflect.DeepEqual(row.AffectedCPUs, []uint32{10}) || row.ScalingCurrentKHz == nil || *row.ScalingCurrentKHz != 9007199254740993 || row.HardwareMinKHz == nil || *row.HardwareMinKHz != 0 || row.HardwareMaxKHz == nil || *row.HardwareMaxKHz != 1<<64-1 || row.Driver == nil || *row.Driver != "driver" || row.Governor == nil || *row.Governor != "governor" || !reflect.DeepEqual(row.AvailableKHz, []uint64{900000, 1800000}) || !reflect.DeepEqual(row.AvailableGovernors, []string{"performance", "powersave"}) {
		t.Fatalf("lost frequency facts: %+v", row)
	}
	if row.HardwareCurrentKHz != nil || row.Unavailable[0] != (domain.FrequencyAttribute{Attribute: "cpuinfo_cur_freq", Reason: "not_present"}) {
		t.Fatalf("fabricated unsupported reading: %+v", row)
	}
	if got.Policies[0].AffectedCPUs == nil || len(got.Policies[0].AffectedCPUs) != 0 {
		t.Fatal("lost known empty online set")
	}
}

func TestFrequencyInterfaceAbsenceAndFailures(t *testing.T) {
	root := t.TempDir()
	got, err := readFrequencies(context.Background(), filepath.Join(root, "missing"))
	if err != nil || got.InterfacePresent || got.Policies == nil || len(got.Policies) != 0 {
		t.Fatalf("missing interface: %+v %v", got, err)
	}
	got, err = readFrequencies(context.Background(), root)
	if err != nil || !got.InterfacePresent || len(got.Policies) != 0 {
		t.Fatalf("empty interface: %+v %v", got, err)
	}
	for _, attributes := range []map[string]string{
		{"related_cpus": "0 0", "affected_cpus": "0"},
		{"related_cpus": "", "affected_cpus": ""},
		{"related_cpus": "4294967296", "affected_cpus": "0"},
		{"related_cpus": "0", "affected_cpus": "0", "scaling_cur_freq": "-1"},
		{"related_cpus": "0", "affected_cpus": "0", "scaling_cur_freq": "18446744073709551616"},
		{"related_cpus": "0", "affected_cpus": "0", "scaling_available_frequencies": "1 broken"},
		{"related_cpus": "0", "affected_cpus": "0", "scaling_driver": strings.Repeat("x", (64<<10)+1)},
		{"related_cpus": "0", "affected_cpus": "0", "scaling_driver": string([]byte{255})},
	} {
		fixture := frequencyFixture(t, map[string]map[string]string{"policy0": attributes})
		result, err := readFrequencies(context.Background(), fixture)
		if err == nil || result.Policies != nil {
			t.Fatalf("invalid policy accepted: %+v %v", result, err)
		}
	}
	missing := frequencyFixture(t, map[string]map[string]string{"policy0": {"related_cpus": "0"}})
	_, err = readFrequencies(context.Background(), missing)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost disappearing policy cause: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := readFrequencies(ctx, root); !errors.Is(err, context.Canceled) || got.Policies != nil {
		t.Fatalf("canceled inventory: %+v %v", got, err)
	}
}

func TestFrequencyPolicyConcurrencyIsBoundedOrderedAndJoined(t *testing.T) {
	ids := make([]uint32, 64)
	for i := range ids {
		ids[i] = uint32(i)
	}
	var active, peak, calls atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		rows, err := readFrequencyPolicies(context.Background(), ids, func(ctx context.Context, id uint32) (domain.FrequencyPolicy, error) {
			count := active.Add(1)
			defer active.Add(-1)
			calls.Add(1)
			for old := peak.Load(); count > old && !peak.CompareAndSwap(old, count); old = peak.Load() {
			}
			<-release
			return domain.FrequencyPolicy{ID: id}, nil
		})
		if err != nil || len(rows) != len(ids) {
			t.Errorf("bounded read: %d %v", len(rows), err)
			return
		}
		for i, row := range rows {
			if row.ID != uint32(i) {
				t.Errorf("lost policy order: %+v", rows)
				return
			}
		}
	}()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for active.Load() != frequencyConcurrency {
		select {
		case <-deadline.C:
			t.Fatal("native reads did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("policy reads did not join")
	}
	if peak.Load() != frequencyConcurrency || active.Load() != 0 || calls.Load() != 64 {
		t.Fatalf("unbounded/incomplete work: peak=%d active=%d calls=%d", peak.Load(), active.Load(), calls.Load())
	}
	cause := errors.New("native failure")
	rows, err := readFrequencyPolicies(context.Background(), ids, func(ctx context.Context, id uint32) (domain.FrequencyPolicy, error) {
		active.Add(1)
		defer active.Add(-1)
		if id == 0 {
			return domain.FrequencyPolicy{}, cause
		}
		<-ctx.Done()
		return domain.FrequencyPolicy{}, ctx.Err()
	})
	if rows != nil || !errors.Is(err, cause) || active.Load() != 0 {
		t.Fatalf("failure lost/join incomplete: %v %v %d", rows, err, active.Load())
	}
}

func TestFrequencyAttributeIssuesAndReadCauses(t *testing.T) {
	root := frequencyFixture(t, map[string]map[string]string{"policy0": {"cpuinfo_cur_freq": "1"}})
	path := filepath.Join(root, "policy0", "cpuinfo_cur_freq")
	if os.Geteuid() != 0 {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		issues := []domain.FrequencyAttribute{}
		_, ok, err := frequencyAttribute(context.Background(), filepath.Dir(path), "cpuinfo_cur_freq", &issues)
		if err != nil || ok || !reflect.DeepEqual(issues, []domain.FrequencyAttribute{{Attribute: "cpuinfo_cur_freq", Reason: "permission_denied"}}) {
			t.Fatalf("permission erased: %v %v %v", issues, ok, err)
		}
	}
	_, err := frequencyText(context.Background(), filepath.Join(root, "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	// malformed numbers retain strconv's typed cause rather than string dispatch.
	bad := frequencyFixture(t, map[string]map[string]string{"policy0": {"related_cpus": "0", "affected_cpus": "0", "scaling_cur_freq": "bad"}})
	_, err = readFrequencies(context.Background(), bad)
	var number *strconv.NumError
	if !errors.As(err, &number) {
		t.Fatalf("lost numeric cause: %v", err)
	}
}
