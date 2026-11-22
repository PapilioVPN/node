//go:build integration && linux

package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	output "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	instancesv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestNativeTailFollowReconnectAndRedactionAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	b := broker(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, b, transport)
			n := startNode(t, f)
			raw, err := n.source.ReadOutput(context.Background(), output.OutputRequest{})
			if err != nil || !strings.Contains(string(raw.Data), secret) {
				t.Fatal("fixture did not produce sensitive native text", err)
			}
			time.Sleep(150 * time.Millisecond)
			first := pull(t, n, transport, "", 0)
			if len(first.Records) == 0 {
				t.Fatal("empty native startup tail")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			type response struct {
				data []byte
				err  error
			}
			arrived := make(chan response, 1)
			joined := make(chan struct{})
			before := n.calls.Load()
			go func() {
				defer close(joined)
				data, err := request(ctx, n, transport, first.NextCursor, 1000, nil)
				arrived <- response{data, err}
			}()
			t.Cleanup(func() { cancel(); <-joined })
			await(t, func() bool { return n.calls.Load() > before })
			traffic(t, f, secret+".invalid:80")
			received := <-arrived
			if received.err != nil {
				t.Fatal(received.err)
			}
			next := decodePage(t, received.data)
			await(t, func() bool {
				raw, err := n.source.ReadOutput(context.Background(), output.OutputRequest{})
				return err == nil && strings.Contains(string(raw.Data), secret+".invalid")
			})

			if len(next.Records) == 0 || next.ExecutionID != first.ExecutionID {
				t.Fatal("follow missed actual native output", next)
			}
			// Repeating the last received position after a lost reply/reconnection is a
			// read-only replay. New output can extend the page but cannot change its prefix.
			reconnect(t, n, f, transport)
			replay := pull(t, n, transport, first.NextCursor, 0)
			if len(replay.Records) < len(next.Records) || replay.Records[0] != next.Records[0] {
				t.Fatal("resume lost stable prefix")
			}
			calls := n.calls.Load()
			traffic(t, f, secret+"-slow.invalid:80")
			time.Sleep(30 * time.Millisecond)
			if n.calls.Load() != calls {
				t.Fatal("background per-consumer read or queue appeared")
			}
			slow := pull(t, n, transport, replay.NextCursor, 1000)
			if len(slow.Records) == 0 {
				t.Fatal("slow consumer lost retained records")
			}
		})
	}
}
func TestNativeExecutionGapAndReattachment(t *testing.T) {
	pki := testutil.NewPKI(t)
	f := newFixture(t, pki, broker(t, pki), "all")
	n := startNode(t, f)
	original := pull(t, n, "grpc", "", 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := instancesv1.NewInstancesServiceClient(n.grpc).SetInstanceRetention(ctx, &instancesv1.SetInstanceRetentionRequest{InstanceId: "edge", ExecutionId: original.ExecutionID, Preserve: true})
	if err != nil {
		t.Fatal(err)
	}
	n.stop(t)
	n = startNode(t, f)
	resumed := pull(t, n, "grpc", original.NextCursor, 0)
	if resumed.ExecutionID != original.ExecutionID || !resumed.Reattached || resumed.Generation != original.Generation {
		t.Fatal("reattachment did not preserve cursor", resumed)
	}

	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	if _, err := instancesv1.NewInstancesServiceClient(n.grpc).StopInstance(ctx, &instancesv1.StopInstanceRequest{InstanceId: "edge"}); err != nil {
		t.Fatal(err)
	}
	stop()
	exited := pull(t, n, "grpc", resumed.NextCursor, 0)
	uncertain := false
	for _, gap := range exited.Gaps {
		if gap.Reason == "position_uncertain" && !gap.ExtentKnown {
			uncertain = true
		}
	}
	if !exited.Exited || !exited.PositionUncertain || !uncertain {
		t.Fatal("reattached exit hid uncertain final output", exited)
	}
	restart(t, n)
	for _, transport := range []string{"http", "grpc", "nats"} {
		changed := pull(t, n, transport, original.NextCursor, 0)
		if changed.ExecutionID == original.ExecutionID || len(changed.Gaps) == 0 || changed.Gaps[0].Reason != "execution_changed" || changed.Gaps[0].PreviousExecutionID != original.ExecutionID {
			t.Fatal("replacement hid output gap", changed)
		}
	}
}
func TestNativeFollowCancellationAndAbandonedRequestsAreBounded(t *testing.T) {
	pki := testutil.NewPKI(t)
	b := broker(t, pki)
	f := newFixture(t, pki, b, "all")
	n := startNode(t, f)
	cursor := pull(t, n, "grpc", "", 0).NextCursor
	for _, transport := range []string{"grpc", "http", "nats"} {
		t.Run(transport, func(t *testing.T) {
			// Drain delayed startup/native logging before measuring a pending pull.
			time.Sleep(150 * time.Millisecond)
			cursor = pull(t, n, transport, "", 0).NextCursor
			before := n.calls.Load()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			headers := nats.Header(nil)
			if transport == "nats" {
				headers = nats.Header{"Papilio-Deadline": []string{time.Now().Add(150 * time.Millisecond).Format(time.RFC3339Nano)}}
			}
			joined := make(chan struct{})
			go func() { defer close(joined); _, err := request(ctx, n, transport, cursor, 1000, headers); done <- err }()
			t.Cleanup(func() { cancel(); <-joined })
			await(t, func() bool { return n.calls.Load() > before })
			cancel()
			err := <-done
			if transport == "grpc" {
				if status.Code(err) != codes.Canceled {
					t.Fatal(err)
				}
			} else if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			// Fiber and Core NATS do not deliver peer cancellation to the handler. Their
			// pull lease/request deadline still releases all work without a subscriber.
			if transport == "http" {
				time.Sleep(1100 * time.Millisecond)
			} else {
				time.Sleep(200 * time.Millisecond)
			}
			settled := n.calls.Load()
			time.Sleep(150 * time.Millisecond)
			if n.calls.Load() != settled {
				t.Fatal("abandoned pull kept polling native output")
			}
			pull(t, n, transport, cursor, 0)
		})
	}
}
func TestNativeSlowConsumerSeesPrefixCapacityLoss(t *testing.T) {
	pki := testutil.NewPKI(t)
	f := newFixture(t, pki, broker(t, pki), "all")
	n := startNode(t, f)
	first := pull(t, n, "grpc", "", 0)
	reached := false
	for i := 0; i < 250; i++ {
		traffic(t, f, strings.Repeat("s", 8192)+".invalid:80")
		raw, err := n.source.ReadOutput(context.Background(), output.OutputRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if raw.CapacityReached {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatal("real Xray did not fill the owned console prefix")
	}
	for _, transport := range []string{"http", "grpc", "nats"} {
		p := pull(t, n, transport, first.NextCursor, 0)
		found := false
		for _, gap := range p.Gaps {
			if gap.Reason == "source_capacity_reached" && !gap.ExtentKnown {
				found = true
			}
		}
		if !found || !p.CapacityReached {
			t.Fatal("slow consumer received silent loss", p)
		}
		// Drain the retained prefix despite the unknown loss after its end.
		// Each continuation makes one bounded source read, never a full tail scan.
		for pages := 0; ; pages++ {
			if pages > 100 {
				t.Fatal("retained prefix did not finish")
			}
			previous := p.NextCursor
			before := n.calls.Load()
			p = pull(t, n, transport, previous, 0)
			if n.calls.Load()-before != 1 {
				t.Fatal("continuation rescanned output")
			}
			if p.NextCursor == previous {
				if len(p.Records) != 0 {
					t.Fatal("nonadvancing records")
				}
				break
			}
		}
	}
	tail := pull(t, n, "http", "", 0)
	if len(tail.Records) > 50 || !tail.CapacityReached {
		t.Fatal(tail)
	}
}
