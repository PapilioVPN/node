package natsserver

import (
	"context"
	"net"
)

// nats.go's dialer has no context argument. Initial INFO/TLS setup also needs
// cancellation after Dial returns. Reconnection is disabled for this connection.
type startupDialer struct {
	ctx  context.Context
	stop func() bool
	done chan struct{}
}

func (d *startupDialer) Dial(network, address string) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(d.ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.done = make(chan struct{})
	d.stop = context.AfterFunc(d.ctx, func() { _ = connection.Close(); close(d.done) })
	return connection, nil
}

func (d *startupDialer) release() {
	if d.stop != nil && !d.stop() {
		<-d.done
	}
}
