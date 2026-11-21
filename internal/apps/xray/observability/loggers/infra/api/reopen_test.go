package api

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type connectionFixture struct {
	noPeer bool
	calls  int
	err    error
}

func (c *connectionFixture) Invoke(_ context.Context, method string, input, output any, options ...grpc.CallOption) error {
	c.calls++
	if !c.noPeer {
		for _, option := range options {
			if capture, ok := option.(grpc.PeerCallOption); ok {
				capture.PeerAddr.Addr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
			}
		}
	}

	if method != "/xray.app.log.command.LoggerService/RestartLogger" {
		return errors.New("unexpected native method")
	}
	return c.err
}
func (c *connectionFixture) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	panic("logger restart must be unary")
}
func TestNativeRestartSeparatesTerminalErrorsAndUnresolvedEffects(t *testing.T) {
	connection := &connectionFixture{}
	restarter := New(connection)
	for _, test := range []struct {
		cause         error
		effect        domain.Effect
		ack, requires bool
	}{{nil, domain.Completed, true, false}, {rpc.ErrClosed, domain.NotApplied, false, false}, {status.Error(codes.Unimplemented, "unavailable method"), domain.NotApplied, false, false}, {status.Error(codes.Unknown, "private native file error"), domain.Unknown, false, false}, {status.Error(codes.Unavailable, "lost connection"), domain.Unknown, false, true}, {context.DeadlineExceeded, domain.Unknown, false, true}} {
		connection.err = test.cause
		result, err := restarter.Reopen(context.Background())
		if result.Effect != test.effect || result.Acknowledged != test.ack || result.RequiresInstanceRestart != test.requires || (test.cause != nil && !errors.Is(err, test.cause)) {
			t.Fatalf("native outcome %+v %v", result, err)
		}
	}
	if connection.calls != 6 {
		t.Fatal("native mutation was replayed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := restarter.Reopen(ctx); !errors.Is(err, context.Canceled) || result.Effect != domain.NotApplied || connection.calls != 6 {
		t.Fatal("pre-canceled operation was dispatched", err)
	}
}

func TestNoNativeTransportDoesNotFenceAnUnsentRequest(t *testing.T) {
	connection := &connectionFixture{noPeer: true, err: status.Error(codes.Unavailable, "initial connection refused")}
	result, err := New(connection).Reopen(context.Background())
	if err == nil || result.Effect != domain.NotApplied || result.RequiresInstanceRestart || connection.calls != 1 {
		t.Fatalf("unsent mutation falsely blocked recovery %+v %v", result, err)
	}
}
