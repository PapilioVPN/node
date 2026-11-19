// Package resources observes only installed Node communication resources.
package resources

import (
	"context"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
	"github.com/PapilioVPN/node/internal/platform/grpcserver"
	"github.com/PapilioVPN/node/internal/platform/httpserver"
	"github.com/PapilioVPN/node/internal/platform/natsclient"
)

type Reader struct {
	HTTP *httpserver.State
	GRPC *grpcserver.State
	NATS *natsclient.Client
}

func (reader Reader) Read(ctx context.Context) (domain.Diagnostics, error) {
	if err := ctx.Err(); err != nil {
		return domain.Diagnostics{}, err
	}
	result := domain.Diagnostics{}
	if reader.HTTP != nil {
		result.HTTP = &domain.Listener{Ready: reader.HTTP.Ready()}
	}
	if reader.GRPC != nil {
		result.GRPC = &domain.Listener{Ready: reader.GRPC.Ready()}
	}
	if reader.NATS != nil {
		broker := &domain.Broker{}
		result.NATS = broker
		connection, err := reader.NATS.Conn()
		if err != nil {
			broker.State = "not_started"
			broker.ErrorCode = "not_started"
			broker.Cause = err
			return result, nil
		}
		broker.State = connection.Status().String()
		if connection.IsConnected() {
			start := time.Now()
			err := connection.FlushWithContext(ctx)
			if err == nil {
				broker.ProbeSucceeded = true
				broker.RoundTrip = time.Since(start)
			} else {
				broker.ErrorCode = "unavailable"
				broker.Cause = err
			}
		} else {
			broker.ErrorCode = "not_connected"
		}
		stats := connection.Stats()
		broker.InMessages = stats.InMsgs
		broker.OutMessages = stats.OutMsgs
		broker.InBytes = stats.InBytes
		broker.OutBytes = stats.OutBytes
		broker.Reconnects = stats.Reconnects
	}
	if err := ctx.Err(); err != nil {
		return domain.Diagnostics{}, err
	}
	return result, nil
}
