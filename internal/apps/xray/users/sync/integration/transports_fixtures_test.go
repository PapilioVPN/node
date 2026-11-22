//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	syncmodule "github.com/PapilioVPN/node/internal/apps/xray/users/sync"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/wire"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/shared/mtls"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
)

type addresses struct {
	slog.Handler
	mu     sync.Mutex
	values map[string]string
}

func (a *addresses) Handle(_ context.Context, r slog.Record) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	r.Attrs(func(attr slog.Attr) bool {
		if attr.Key == "address" {
			a.values[r.Message] = attr.Value.String()
		}
		return true
	})
	return nil
}

type operator struct {
	kind    string
	http    *http.Client
	address string
	grpc    v1.SyncServiceClient
	nats    *nats.Conn
}

func startTransport(t *testing.T, f *fixture, kind string, pki testutil.PKI, broker string) *operator {
	t.Helper()
	log := &addresses{Handler: slog.NewTextHandler(io.Discard, nil), values: make(map[string]string)}
	opts := []fx.Option{fx.NopLogger, fx.Supply(slog.New(log)), syncmodule.TransportModule(settings.Requests{Timeout: 2 * time.Second}, fx.Supply(f.service, services.NewTransfers(), fx.Private))}
	switch kind {
	case "http":
		opts = append(opts, fx.Supply(settings.HTTP{Address: "127.0.0.1:0", TLS: pki.Server, ReadTimeout: 2 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: time.Second, ShutdownTimeout: time.Second, BodyLimit: 2 << 20, Concurrency: 8}), httpserver.Module)
	case "grpc":
		opts = append(opts, fx.Supply(settings.GRPC{Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 100 * time.Millisecond, ShutdownTimeout: time.Second, MaxRecvMessageSize: 2 << 20, MaxSendMessageSize: 2 << 20, MaxConnections: 8, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192}), grpcserver.Module)
	case "nats":
		opts = append(opts, fx.Supply(settings.NATS{Servers: []string{broker}, TLS: pki.Client, TLSHandshakeFirst: true, ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second, ReconnectWait: time.Second, ReconnectBufferSize: -1, PingInterval: time.Second, MaxPingsOutstanding: 2}), natsclient.Module, natsclient.RequestsModule(settings.NATSRequests{SubjectPrefix: "fixture.sync", PendingMessages: 8, PendingBytes: 2 << 20, ShutdownTimeout: 10 * time.Millisecond}, time.Second))
	}
	app := fx.New(opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		if err := app.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return connect(t, kind, pki, broker, log)
}

func connect(t *testing.T, kind string, pki testutil.PKI, broker string, log *addresses) *operator {
	t.Helper()
	identity, err := mtls.Client(pki.Client)
	if err != nil {
		t.Fatal(err)
	}
	o := &operator{kind: kind}
	log.mu.Lock()
	defer log.mu.Unlock()
	switch kind {
	case "http":
		transport := &http.Transport{TLSClientConfig: identity}
		t.Cleanup(transport.CloseIdleConnections)
		o.http = &http.Client{Transport: transport, Timeout: 3 * time.Second}
		o.address = "https://" + log.values["HTTP listening"]
	case "grpc":
		connection, err := grpc.NewClient(log.values["gRPC listening"], grpc.WithTransportCredentials(credentials.NewTLS(identity)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = connection.Close() })
		o.grpc = v1.NewSyncServiceClient(connection)
	case "nats":
		connection, err := nats.Connect(broker, nats.Secure(identity), nats.TLSHandshakeFirst(), nats.NoReconnect())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(connection.Close)
		o.nats = connection
	}
	return o
}
func (o *operator) json(t *testing.T, method, path, subject string, input, output proto.Message) {
	t.Helper()
	var data []byte
	var err error
	if input != nil && input.ProtoReflect().IsValid() {
		data, err = wire.Encode(input)
		if err != nil {
			t.Fatal(err)
		}
	}
	if o.kind == "http" {
		request, err := http.NewRequest(method, o.address+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if input != nil && input.ProtoReflect().IsValid() {
			request.Header.Set("Content-Type", "application/json")
		}
		response, err := o.http.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err = io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil || response.StatusCode != 200 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP status=%d body=%s err=%v", response.StatusCode, data, err)
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		response, err := o.nats.RequestWithContext(ctx, "fixture.sync."+subject, data)
		if err != nil {
			t.Fatal(err)
		}
		data = response.Data
	}
	if strings.Contains(string(data), "configuration") || strings.Contains(string(data), "secret") {
		t.Fatal("credential material in public result")
	}
	if err := wire.Decode(data, output); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}
func (o *operator) revision(t *testing.T) *v1.Revision {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if o.grpc != nil {
		r, err := o.grpc.GetSyncRevision(ctx, &v1.InstanceRequest{InstanceId: "one"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	var r v1.Revision
	var input *v1.InstanceRequest
	if o.kind != "http" {
		input = &v1.InstanceRequest{InstanceId: "one"}
	}
	o.json(t, "GET", "/v1/xray/users/instances/one/sync/revision", "xray.users.sync.v1.revision", input, &r)
	return &r
}
