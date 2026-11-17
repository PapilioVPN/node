//go:build integration

package bootstrap_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/fx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	nodev1 "github.com/PapilioVPN/node/internal/apps/system/node/info/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/bootstrap"
	rpc "github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func TestNATSOnlyAndAllTransportsShareNodeInfo(t *testing.T) {
	for _, all := range []bool{false, true} {
		name := "nats-only"
		if all {
			name = "all-transports"
		}
		t.Run(name, func(t *testing.T) {
			broker := testutil.NewNATSBroker(t)
			cfg := httpConfig(broker.Certificates)
			cfg.Communications.GRPC.Enabled, cfg.Communications.HTTP.Enabled = all, all
			cfg.Communications.NATS = broker.Config()
			var grpcServer *rpc.Server
			var httpServer *httpserver.Server
			options := []fx.Option{fx.NopLogger}
			if all {
				options = append(options, fx.Populate(&grpcServer, &httpServer))
			}
			app := bootstrap.New(cfg, "shared-version", options...)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := app.Start(ctx); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.Stop(context.Background()) })
			client := broker.Client(t, "controller")
			response, err := client.RequestWithContext(ctx, cfg.Communications.NATS.SubjectPrefix+".node.info.get", []byte("{}"))
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				NodeID       string   `json:"node_id"`
				Version      string   `json:"version"`
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal(response.Data, &result); err != nil {
				t.Fatal(err)
			}
			if result.NodeID != cfg.Node.ID || result.Version != "shared-version" || len(result.Capabilities) < 2 {
				t.Fatalf("wrong NATS composition: %s", response.Data)
			}
			if all {
				conn, err := grpc.NewClient(grpcServer.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(broker.Certificates.ClientTLS(t))))
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				info, err := nodev1.NewNodeServiceClient(conn).GetNodeInfo(ctx, &nodev1.GetNodeInfoRequest{})
				if err != nil || info.GetNodeId() != result.NodeID || info.GetVersion() != result.Version || !slices.Equal(info.GetCapabilities(), result.Capabilities) {
					t.Fatalf("transports disagree: %v %v", info, err)
				}
				transport := &http.Transport{TLSClientConfig: broker.Certificates.ClientTLS(t)}
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport, Timeout: time.Second}
				response, err := client.Get("https://" + httpServer.Addr().String() + "/v1/node")
				if err != nil {
					t.Fatal(err)
				}
				var httpInfo struct {
					NodeID       string   `json:"node_id"`
					Version      string   `json:"version"`
					Capabilities []string `json:"capabilities"`
				}
				decodeErr := json.NewDecoder(response.Body).Decode(&httpInfo)
				response.Body.Close()
				transport.CloseIdleConnections()
				if decodeErr != nil || response.StatusCode != 200 || httpInfo.NodeID != result.NodeID || httpInfo.Version != result.Version || !slices.Equal(httpInfo.Capabilities, result.Capabilities) {
					t.Fatalf("HTTP/NATS facts differ: %+v %v", httpInfo, decodeErr)
				}

			}
			if err := app.Stop(ctx); err != nil {
				t.Fatal(err)
			}
			if all {
				for _, address := range []string{grpcServer.Addr().String(), httpServer.Addr().String()} {
					listener, err := net.Listen("tcp", address)
					if err != nil {
						t.Fatal(err)
					}
					listener.Close()
				}
			}
		})
	}
}

func TestBrokerACLRejectsUnprivilegedPublisher(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := settings.Config{Node: settings.Node{ID: "acl-node"}, Runtime: settings.Runtime{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, Communications: settings.Communications{NATS: broker.Config()}}
	app := bootstrap.New(cfg, "test", fx.NopLogger)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
	rejected := make(chan error, 1)
	outsider := broker.Client(t, "outsider", nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
		select {
		case rejected <- err:
		default:
		}
	}))
	request, release := context.WithTimeout(ctx, 50*time.Millisecond)
	defer release()
	_, err := outsider.RequestWithContext(request, cfg.Communications.NATS.SubjectPrefix+".node.info.get", []byte("{}"))
	if err == nil {
		t.Fatal("broker ACL permitted an unprivileged publisher")
	}
	select {
	case err := <-rejected:
		if !errors.Is(err, nats.ErrPermissionViolation) {
			t.Fatalf("unexpected broker rejection: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("no native ACL rejection observed")
	}
}

func TestCanceledStartupClosesThreeTransportsAndInstance(t *testing.T) {
	broker := testutil.NewNATSBroker(t)
	cfg := httpConfig(broker.Certificates)
	cfg.Communications.GRPC.Enabled = true
	cfg.Communications.NATS = broker.Config()
	instance, engineAddress := testutil.ConfiguredXray(t, t.TempDir(), "owned-engine")
	cfg.Instances = []settings.Instance{instance}
	client := broker.Client(t, "controller")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var addresses []string
	err := bootstrap.Run(ctx, cfg, "test", fx.NopLogger, fx.Invoke(func(lifecycle fx.Lifecycle, httpServer *httpserver.Server, grpcServer *rpc.Server) {
		lifecycle.Append(fx.Hook{OnStart: func(startup context.Context) error {
			addresses = append(addresses, httpServer.Addr().String(), grpcServer.Addr().String())
			if err := testutil.XrayTraffic(startup, engineAddress); err != nil {
				return err
			}
			if _, err := client.RequestWithContext(startup, cfg.Communications.NATS.SubjectPrefix+".node.info.get", []byte("{}")); err != nil {
				return err
			}
			cancel()
			return context.Canceled
		}})
	}))
	if !errors.Is(err, context.Canceled) || len(addresses) != 2 {
		t.Fatalf("canceled startup lost cause: %v", err)
	}
	for _, address := range addresses {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("transport leaked on canceled startup: %v", err)
		}
		listener.Close()
	}
	testutil.AssertTCPPortsReleased(t, engineAddress)
	request, release := context.WithTimeout(context.Background(), time.Second)
	defer release()
	if _, err := client.RequestWithContext(request, cfg.Communications.NATS.SubjectPrefix+".node.info.get", []byte("{}")); !errors.Is(err, nats.ErrNoResponders) {
		t.Fatalf("canceled startup retained broker subscription: %v", err)
	}
}
