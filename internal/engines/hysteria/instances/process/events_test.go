package process

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/hysteria/logs/live"
)

func TestOnlyCanonicalNativeEventsArePublished(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	j := live.NewJournal(ctx, 4)
	s := live.New(func(context.Context, string) (*live.Journal, error) { return j, nil })
	sub, err := s.Subscribe(ctx, "edge")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	output := &events{ready: make(chan struct{}), journal: j}
	_, _ = io.WriteString(output, `{"message":"client connected","msg":"private fixture log"}`+"\n"+`{"msg":"client con`)
	_, _ = io.WriteString(output, `nected","id":"fixture-private-auth-id","error":"fixture-private-secret"}`+"\n")
	read, done := context.WithTimeout(ctx, time.Second)
	defer done()
	row, err := sub.Next(read)
	if err != nil || row.Name != "client_connected" || row.Sequence != 1 || row.Execution != 4 {
		t.Fatalf("unexpected canonical event: %+v, %v", row, err)
	}
	cancel()
	if _, err := sub.Next(ctx); err != io.EOF && err != context.Canceled {
		t.Fatalf("execution end: %v", err)
	}
}
