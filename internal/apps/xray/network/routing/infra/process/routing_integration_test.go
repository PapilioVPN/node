//go:build integration && linux

package process_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	router "github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/geodata"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	xrayprocess "github.com/PapilioVPN/node/internal/apps/xray/network/routing/infra/process"
)

func rulePreparer(t *testing.T, cfg xrayprocess.RoutingRulePreparationConfig) *xrayprocess.RoutingRulePreparer {
	t.Helper()
	p, err := xrayprocess.NewRoutingRulePreparer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func rulePreparationConfig(t *testing.T) xrayprocess.RoutingRulePreparationConfig {
	t.Helper()
	return xrayprocess.RoutingRulePreparationConfig{Binary: xrayBinary(t), Directory: t.TempDir(), WorkDir: t.TempDir(), Timeout: 2 * time.Second, MaxInputBytes: 65536, MaxOutputBytes: 65536, MaxConcurrent: 1}
}

func TestNativeRulePreparationPreservesFieldsAndDoesNotConstructEngine(t *testing.T) {
	cfg := rulePreparationConfig(t)
	var effects atomic.Int32
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { effects.Add(1); w.WriteHeader(204) }))
	defer webhook.Close()
	p := rulePreparer(t, cfg)
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			inbounds := make([]string, count)
			for i := range inbounds {
				inbounds[i] = fmt.Sprint(" in-", i, " ")
			}
			// One native field rule, retaining advanced selectors and webhook settings.
			content := fmt.Sprintf(`{"ruleTag":" exact>>>rule ","outboundTag":" out ","domain":["full:Case.invalid"],"ip":["192.0.2.0/24"],"sourceIP":["2001:db8::/32"],"sourcePort":"1-65535","localIP":["127.0.0.0/8"],"localPort":65535,"port":"443","network":"tcp,udp","user":[" Mixed@Example "],"vlessRoute":"65535","protocol":["http"],"attrs":{" key ":" value "},"process":["process-name"],"localOS":["linux"],"webhook":{"url":%q,"headers":{"X-Exact":" value "},"deduplication":17},"inboundTag":%s}`, webhook.URL, mustJSON(t, inbounds))
			prepared, err := p.PrepareRoutingRule(t.Context(), []byte(content))
			if err != nil || prepared.RuleTag != " exact>>>rule " || len(prepared.Config) == 0 {
				t.Fatalf("native rule not prepared: %+v %v", prepared, err)
			}
			var compiled router.Config
			if err := proto.Unmarshal(prepared.Config, &compiled); err != nil || len(compiled.Rule) != 1 {
				t.Fatalf("not one canonical router.Config: %v", err)
			}
			r := compiled.Rule[0]
			if r.RuleTag != " exact>>>rule " || r.GetTag() != " out " || len(r.InboundTag) != count || len(r.Domain) != 1 || len(r.Ip) != 1 || len(r.SourceIp) != 1 || len(r.LocalIp) != 1 || r.SourcePortList == nil || r.LocalPortList == nil || r.PortList == nil || r.VlessRouteList == nil || len(r.Networks) != 2 || r.UserEmail[0] != " Mixed@Example " || r.Protocol[0] != "http" || r.Attributes[" key "] != " value " || r.Process[0] != "process-name" || r.LocalOs[0] != "linux" || r.Webhook.GetUrl() != webhook.URL || r.Webhook.Headers["X-Exact"] != " value " || r.Webhook.Deduplication != 17 {
				t.Fatalf("native fields were narrowed/changed: %v", r)
			}
			for i, value := range r.InboundTag {
				if value != inbounds[i] {
					t.Fatal("native list order/identity changed")
				}
			}
			assertEmptyDirectory(t, cfg.WorkDir)
		})
	}
	if effects.Load() != 0 {
		t.Fatal("preparation constructed/fired native webhooks")
	}
	for _, content := range []string{`{"outboundTag":"out","network":"tcp"}`, `{"ruleTag":"   ","outboundTag":"out","network":"tcp"}`, `{"balancerTag":" existing-pool ","network":"tcp"}`} {
		if _, err := p.PrepareRoutingRule(t.Context(), []byte(content)); err != nil {
			t.Fatalf("anonymous/whitespace/balancer rule rejected before native application: %v", err)
		}
	}
	for _, content := range []string{`{`, `null`, `[]`, `{}`, `{"network":"tcp"}`, `{"outboundTag":"out","port":"invalid"}`} {
		prepared, err := p.PrepareRoutingRule(t.Context(), []byte(content))
		if !errors.Is(err, domain.ErrInvalidRoutingRule) || len(prepared.Config) != 0 {
			t.Fatalf("invalid native input returned prepared state: %+v %v", prepared, err)
		}
		assertEmptyDirectory(t, cfg.WorkDir)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestNativeRulePreparationUsesSelectedBinaryAssetsWithoutParentOverrides(t *testing.T) {
	// Each child uses its own executable directory. A hostile/different parent
	// setting must not become a process-global parser hook or leak into children.
	t.Setenv("XRAY_LOCATION_ASSET", t.TempDir())
	for _, name := range []string{"first", "second"} {
		cfg := rulePreparationConfig(t)
		binaryDir := t.TempDir()
		cfg.Binary = filepath.Join(binaryDir, "xray")
		source, err := os.Open(xrayBinary(t))
		if err != nil {
			t.Fatal(err)
		}
		target, err := os.OpenFile(cfg.Binary, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
		if err != nil {
			source.Close()
			t.Fatal(err)
		}
		_, copyError := io.Copy(target, source)
		closeError := errors.Join(source.Close(), target.Close())
		if err := errors.Join(copyError, closeError); err != nil {
			t.Fatal(err)
		}
		site := &geodata.GeoSiteList{Entry: []*geodata.GeoSite{{Code: strings.ToUpper(name), Domain: []*geodata.Domain{{Type: geodata.Domain_Full, Value: name + ".invalid"}}}}}
		content, err := proto.Marshal(site)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binaryDir, "geosite.dat"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		ip := &geodata.GeoIPList{Entry: []*geodata.GeoIP{{Code: strings.ToUpper(name), Cidr: []*geodata.CIDR{{Ip: []byte{192, 0, 2, 0}, Prefix: 24}}}}}
		content, err = proto.Marshal(ip)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(binaryDir, "geoip.dat"), content, 0o600); err != nil {
			t.Fatal(err)
		}
		p := rulePreparer(t, cfg)
		prepared, err := p.PrepareRoutingRule(t.Context(), []byte(fmt.Sprintf(`{"outboundTag":"out","domain":["geosite:%s"],"ip":["geoip:%s"]}`, name, name)))
		if err != nil {
			t.Fatalf("selected instance asset context lost: %v", err)
		}
		var compiled router.Config
		if err := proto.Unmarshal(prepared.Config, &compiled); err != nil {
			t.Fatal(err)
		}
		r := compiled.Rule[0]
		if r.Domain[0].GetGeosite().GetFile() != "geosite.dat" || r.Ip[0].GetGeoip().GetFile() != "geoip.dat" {
			t.Fatal("native asset references were expanded/rewritten")
		}
		other := "first"
		if name == other {
			other = "second"
		}
		if result, err := p.PrepareRoutingRule(t.Context(), []byte(fmt.Sprintf(`{"outboundTag":"out","domain":["geosite:%s"]}`, other))); !errors.Is(err, domain.ErrInvalidRoutingRule) || len(result.Config) != 0 {
			t.Fatalf("asset lookup borrowed another instance: %+v %v", result, err)
		}
		assertEmptyDirectory(t, cfg.WorkDir)
	}
}

func TestNativeRulePreparationBoundsAndCancellationCleanOwnedResources(t *testing.T) {
	cfg := rulePreparationConfig(t)
	cfg.MaxOutputBytes = 1
	if prepared, err := rulePreparer(t, cfg).PrepareRoutingRule(t.Context(), []byte(`{"outboundTag":"out","network":"tcp"}`)); !errors.Is(err, domain.ErrRoutingRuleOutputLimit) || len(prepared.Config) != 0 {
		t.Fatalf("oversized compiled rule escaped bound: %+v %v", prepared, err)
	}
	assertEmptyDirectory(t, cfg.WorkDir)
	cfg = rulePreparationConfig(t)
	cfg.Binary = filepath.Join(cfg.Directory, "missing")
	cfg.MaxInputBytes = 2
	p := rulePreparer(t, cfg)
	for _, test := range []struct {
		content []byte
		cause   error
	}{{nil, domain.ErrInvalidRoutingRule}, {[]byte("123"), domain.ErrRoutingRuleInputLimit}} {
		if _, err := p.PrepareRoutingRule(t.Context(), test.content); !errors.Is(err, test.cause) {
			t.Fatalf("input limit dispatched missing converter: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.PrepareRoutingRule(ctx, []byte("{}")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if prepared, err := p.PrepareRoutingRule(t.Context(), []byte("{}")); !errors.Is(err, os.ErrNotExist) || len(prepared.Config) != 0 {
		t.Fatalf("missing converter became native rejection/success: %+v %v", prepared, err)
	}
	assertEmptyDirectory(t, cfg.WorkDir)
}

// Controlled OS children verify admission, private files and joined process/pipe
// cleanup. They do not claim native Xray parsing behavior.
func TestRulePreparationSaturationCancellationAndDeadlineJoinChild(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(fmt.Sprint(deadline), func(t *testing.T) {
			cfg := rulePreparationConfig(t)
			cfg.Binary = filepath.Join(cfg.Directory, "controlled-builder")
			cfg.Timeout = 5 * time.Second
			if deadline {
				cfg.Timeout = 300 * time.Millisecond
			}
			if err := os.WriteFile(cfg.Binary, []byte("#!/bin/sh\nprintf '%s' \"$$\" > \"$5.pid\"\nexec /bin/sleep 60\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			p := rulePreparer(t, cfg)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan struct{})
			var buildError error
			go func() { defer close(done); _, buildError = p.PrepareRoutingRule(ctx, []byte("{}")) }()
			defer func() { cancel(); <-done }()
			var pid int
			await(t, func() bool {
				paths, err := filepath.Glob(filepath.Join(cfg.WorkDir, "*", "*.pid"))
				if err != nil || len(paths) != 1 {
					return false
				}
				content, err := os.ReadFile(paths[0])
				if err != nil {
					return false
				}
				pid, err = strconv.Atoi(string(content))
				if err != nil {
					return false
				}
				candidate := strings.TrimSuffix(paths[0], ".pid")
				file, err := os.Stat(candidate)
				if err != nil || file.Mode().Perm() != 0o600 {
					t.Error("routing candidate is not private")
				}
				dir, err := os.Stat(filepath.Dir(candidate))
				if err != nil || dir.Mode().Perm() != 0o700 {
					t.Error("routing work directory is not private")
				}
				return true
			})
			if _, err := p.PrepareRoutingRule(t.Context(), []byte("{}")); !errors.Is(err, domain.ErrRoutingRulePreparationBusy) {
				t.Fatalf("saturated builder queued/spawned: %v", err)
			}
			want := context.DeadlineExceeded
			if !deadline {
				cancel()
				want = context.Canceled
			}
			select {
			case <-done:
				if !errors.Is(buildError, want) || errors.Is(buildError, domain.ErrRoutingMutationUnknown) {
					t.Fatalf("preparation cancellation lost stage/cause: %v", buildError)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("output pipe/caller not released")
			}
			if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("converter not joined: %v", err)
			}
			assertEmptyDirectory(t, cfg.WorkDir)
			if err := os.Remove(cfg.Binary); err != nil {
				t.Fatal(err)
			}
			if _, err := p.PrepareRoutingRule(t.Context(), []byte("{}")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("builder slot leaked: %v", err)
			}
		})
	}
}

// A controlled converter isolates output framing, exact ceilings and malformed
// protobuf handling from the real executable tests above.
func TestRulePreparationKeepsNativeBytesAndRejectsIncompleteOutput(t *testing.T) {
	cfg := rulePreparationConfig(t)
	cfg.Binary = filepath.Join(cfg.Directory, "controlled-converter")
	if err := os.WriteFile(cfg.Binary, []byte("#!/bin/sh\nprintf 'unframed diagnostic text'\nexec /bin/cat compiled.pb >&3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	routing := &router.Config{Rule: []*router.RoutingRule{{RuleTag: " exact ", TargetTag: &router.RoutingRule_Tag{Tag: "out"}}}}
	routing.ProtoReflect().SetUnknown(protowire.AppendBytes(protowire.AppendTag(nil, 99, protowire.BytesType), []byte("native future field")))
	message := serial.ToTypedMessage(routing)
	outer, err := proto.Marshal(&core.Config{App: []*serial.TypedMessage{message}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.Directory, "compiled.pb"), outer, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.MaxOutputBytes = len(outer)
	prepared, err := rulePreparer(t, cfg).PrepareRoutingRule(t.Context(), []byte("{}"))
	if err != nil || prepared.RuleTag != " exact " || !bytes.Equal(prepared.Config, message.Value) {
		t.Fatalf("exact ceiling/native bytes/unknown fields changed: %+v %v", prepared, err)
	}
	cfg.MaxOutputBytes--
	if _, err := rulePreparer(t, cfg).PrepareRoutingRule(t.Context(), []byte("{}")); !errors.Is(err, domain.ErrRoutingRuleOutputLimit) {
		t.Fatalf("native output ceiling exceeded: %v", err)
	}
	cfg.MaxOutputBytes = 65536
	emptyRouting := serial.ToTypedMessage(&router.Config{})
	duplicate, err := proto.Marshal(&core.Config{App: []*serial.TypedMessage{message, message}})
	if err != nil {
		t.Fatal(err)
	}
	missingRule, err := proto.Marshal(&core.Config{App: []*serial.TypedMessage{emptyRouting}})
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{nil, {255}, duplicate, missingRule} {
		if err := os.WriteFile(filepath.Join(cfg.Directory, "compiled.pb"), invalid, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := rulePreparer(t, cfg).PrepareRoutingRule(t.Context(), []byte("{}"))
		if !errors.Is(err, domain.ErrInvalidPreparedRoutingRule) || len(result.Config) != 0 {
			t.Fatalf("malformed/partial output became prepared data: %+v %v", result, err)
		}
		assertEmptyDirectory(t, cfg.WorkDir)
	}
}

func xrayBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY is required; run make integration")
	}
	return binary
}

func await(t *testing.T, ready func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !ready() {
		select {
		case <-ctx.Done():
			t.Fatalf("native observation did not complete: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertEmptyDirectory(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("candidate workspace leaked: %v, %v", entries, err)
	}
}
