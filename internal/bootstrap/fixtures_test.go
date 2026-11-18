//go:build integration

package bootstrap_test

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
	"github.com/PapilioVPN/node/internal/testutil"
)

func foundationConfig(pki testutil.PKI, brokerURL string) settings.Config {
	return settings.Config{
		StartupTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second,
		Communications: settings.Communications{Requests: settings.Requests{Timeout: 100 * time.Millisecond}, HTTP: &settings.HTTP{
			Address: "127.0.0.1:0", TLS: pki.Server,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
			IdleTimeout: time.Second, ShutdownTimeout: time.Second,
			BodyLimit: 1024, Concurrency: 16,
		}, GRPC: &settings.GRPC{
			Address: "127.0.0.1:0", TLS: pki.Server, ConnectionTimeout: 200 * time.Millisecond, ShutdownTimeout: time.Second,
			MaxRecvMessageSize: 1024, MaxSendMessageSize: 1024, MaxConnections: 16, MaxConcurrentStreams: 8, MaxHeaderListSize: 8192,
		}, NATS: &settings.NATS{
			Servers: []string{strings.Replace(brokerURL, "nats://", "tls://", 1)}, TLS: pki.Client, TLSHandshakeFirst: true,
			ConnectTimeout: time.Second, ShutdownTimeout: time.Second, WriteTimeout: time.Second,
			ReconnectWait: time.Second, MaxReconnects: 2, ReconnectBufferSize: 1024, PingInterval: time.Second, MaxPingsOutstanding: 2,
		}},
	}
}

type listenerLog struct {
	slog.Handler
	addresses chan string
}

func (log listenerLog) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "HTTP listening" || record.Message == "gRPC listening" {
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "address" {
				log.addresses <- attr.Value.String()
			}
			return true
		})
	}
	return nil
}
