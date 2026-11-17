package natsclient

import (
	"context"
	"net"
	"time"
)

// NATS's CustomDialer has no context argument. This adapter binds both dialing
// and the subsequent native TLS/CONNECT handshake to the resource's lifetime.
type dialer struct {
	context context.Context
	timeout time.Duration
}

func (dialer dialer) Dial(network, address string) (net.Conn, error) {
	conn, err := (&net.Dialer{Timeout: dialer.timeout}).DialContext(dialer.context, network, address)
	if err != nil {
		return nil, err
	}
	return &connection{Conn: conn, stopCancellation: context.AfterFunc(dialer.context, func() { conn.Close() })}, nil
}

type connection struct {
	net.Conn
	stopCancellation func() bool
}

func (conn *connection) Close() error {
	conn.stopCancellation()
	return conn.Conn.Close()
}
