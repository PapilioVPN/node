//go:build integration && linux

package integration_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
)

func TestRealXrayDiagnosticsAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := testutil.NewNATSBroker(t, pki.Server, true)
	address := strings.Replace(broker.ClientURL(), "nats://", "tls://", 1)
	for _, mode := range []string{"healthy", "no-reflection", "no-stats", "disabled", "unreachable", "binary-missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, pki, address, mode)
			for _, transport := range []string{"http", "grpc", "nats"} {
				t.Run(transport, func(t *testing.T) {
					r := f.report(t, transport)
					if (mode != "binary-missing" && (r.Binary.Version != "26.11.9" || r.Binary.Build != "676eaf6")) || r.Binary.Scope != "configured_executable" || r.Execution.Generation != 1 || r.Configuration.DesiredRevision == nil || *r.Configuration.DesiredRevision != 1 || r.Execution.AppliedRevision == nil || *r.Execution.AppliedRevision != 1 || r.Configuration.MatchesApplied == nil || !*r.Configuration.MatchesApplied {
						t.Fatalf("authoritative evidence lost %+v", r)
					}
					switch mode {
					case "binary-missing":
						if r.Binary.State != "unavailable" || r.Binary.Reason != "source_not_found" || r.Native.State != "available" {
							t.Fatal(r.Binary, r.Native)
						}
					case "healthy":
						if r.Native.State != "available" || r.Native.Discovery.State != "available" || r.Native.Runtime.State != "available" || len(r.Native.Capabilities) != 5 {
							t.Fatal(r.Native)
						}
					case "no-reflection":
						if r.Native.State != "available" || r.Native.Discovery.State != "unsupported" || r.Native.Capabilities[0].State != "unknown" {
							t.Fatal(r.Native)
						}
					case "no-stats":
						if r.Native.State != "available" || r.Native.Runtime.State != "unsupported" || r.Native.Discovery.State != "available" {
							t.Fatal(r.Native)
						}
					case "disabled":
						if r.Native.State != "disabled" {
							t.Fatal(r.Native)
						}
					case "unreachable":
						if r.Native.State != "unavailable" {
							t.Fatal(r.Native)
						}
					}
				})
			}
			if mode == "healthy" {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if _, err := f.store.Write(ctx, 1, f.sources); err != nil {
					t.Fatal(err)
				}
				for _, transport := range []string{"http", "grpc", "nats"} {
					r := f.report(t, transport)
					if *r.Configuration.DesiredRevision != 2 || *r.Execution.AppliedRevision != 1 || *r.Configuration.MatchesApplied {
						t.Fatal("desired became applied", r)
					}
				}
				if _, err := f.lifecycle.Restart(ctx, "edge"); err != nil {
					t.Fatal(err)
				}
				for _, transport := range []string{"http", "grpc", "nats"} {
					r := f.report(t, transport)
					if r.Execution.Generation != 2 || *r.Execution.AppliedRevision != 2 || !*r.Configuration.MatchesApplied {
						t.Fatal(r)
					}
				}
				if _, err := f.lifecycle.Stop(ctx, "edge"); err != nil {
					t.Fatal(err)
				}
				for _, transport := range []string{"http", "grpc", "nats"} {
					r := f.report(t, transport)
					if r.Execution.LifecycleState != "stopped" || r.Native.State != "unavailable" {
						t.Fatal(r)
					}
				}
			}
		})
	}
}
