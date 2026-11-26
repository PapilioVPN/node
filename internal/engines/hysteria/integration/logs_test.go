package integration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	iv1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func TestPublicLogStreamsEndWhenExecutionStops(t *testing.T) {
	f := setup(t, "")
	f.request(t, http.MethodPut, "/execution", nil, 200)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, f.http.URL+instance+"/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-API-Key", apiKey)
	resp, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("logs HTTP status %d", resp.StatusCode)
	}
	conn := f.grpcConn(t)
	logs := v1.NewHysteriaLogsServiceClient(conn)
	stream, err := logs.GetLogs(ctx, &v1.HysteriaLogsRequest{Id: "edge"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	if _, err := iv1.NewHysteriaInstancesServiceClient(conn).Stop(ctx, &iv1.HysteriaInstanceRequest{Id: "edge"}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatalf("HTTP log stream did not end cleanly: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) && grpcstatus.Code(err) != codes.Canceled {
		t.Fatalf("gRPC log stream did not end: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("client timeout, not execution stop, ended the stream")
	}
	assertClean(t, f)
}
