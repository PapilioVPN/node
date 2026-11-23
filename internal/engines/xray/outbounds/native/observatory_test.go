package native

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/outbounds/health"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"
)

type execution struct{ conn *grpc.ClientConn }

func (e execution) NativeConnection(string) (*grpc.ClientConn, error) { return e.conn, nil }

func TestOneNativeSnapshotForEverySelection(t *testing.T) {
	var calls atomic.Int32
	listener := bufconn.Listen(64 << 10)
	server := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		method, _ := grpc.MethodFromServerStream(stream)
		if method != getOutboundStatus {
			t.Errorf("native method = %q", method)
		}
		if err := stream.RecvMsg(&emptypb.Empty{}); err != nil {
			return err
		}
		calls.Add(1)
		return stream.SendMsg(&OutboundStatusResponse{Status: &ObservationResult{
			Status: []*OutboundStatus{
				{OutboundTag: "exact-other", Delay: 99999999, LastSeenTime: 5, LastTryTime: 7},
				{OutboundTag: "exact", Alive: true, LastSeenTime: 8, LastTryTime: 8},
			},
		}})
	}))
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && err != grpc.ErrServerStopped {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient("passthrough:///observatory-test",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	service := health.New(NewObservatory(execution{conn: conn}))
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	exact, missing := "exact", "exac"
	for _, test := range []struct {
		tag  *string
		want []health.Status
	}{
		{nil, []health.Status{health.Failed, health.Healthy}},
		{&exact, []health.Status{health.Healthy}},
		{&missing, []health.Status{health.NoSample}},
	} {
		before := calls.Load()
		got, err := service.Get(ctx, health.Request{InstanceID: "primary", Tag: test.tag})
		if err != nil || len(got) != len(test.want) || calls.Load() != before+1 {
			t.Fatalf("tag %v: response %v, error %v, native calls %d", test.tag, got, err, calls.Load()-before)
		}
		for i, status := range test.want {
			row := got[i]
			if row.Status != status {
				t.Fatalf("row %d = %+v, want %v", i, row, status)
			}
			switch status {
			case health.Healthy:
				if row.Tag != exact || row.DelayMilliseconds == nil || *row.DelayMilliseconds != 0 {
					t.Fatalf("healthy zero delay was lost in %+v", row)
				}
			case health.Failed:
				if row.LastSeenUnixSeconds == nil || *row.LastSeenUnixSeconds != 5 ||
					row.LastTryUnixSeconds == nil || *row.LastTryUnixSeconds != 7 {
					t.Fatalf("previous success was lost in %+v", row)
				}
			case health.NoSample:
				if row.Tag != missing || row.DelayMilliseconds != nil {
					t.Fatalf("absence returned sample data: %+v", row)
				}
			}
		}
	}
}
