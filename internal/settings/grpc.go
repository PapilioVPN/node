package settings

import (
	"errors"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
)

type GRPC struct {
	Address              string        `yaml:"address"`
	TLS                  mtls.Files    `yaml:"tls"`
	ConnectionTimeout    time.Duration `yaml:"connection_timeout"`
	ShutdownTimeout      time.Duration `yaml:"shutdown_timeout"`
	MaxRecvMessageSize   int           `yaml:"max_recv_message_size"`
	MaxSendMessageSize   int           `yaml:"max_send_message_size"`
	MaxConnections       int           `yaml:"max_connections"`
	MaxConcurrentStreams uint32        `yaml:"max_concurrent_streams"`
	MaxHeaderListSize    uint32        `yaml:"max_header_list_size"`
}

func (config GRPC) validate() error {
	if err := validateAddress(config.Address); err != nil {
		return err
	}
	if err := validateTLS(config.TLS); err != nil {
		return err
	}
	if config.ConnectionTimeout <= 0 || config.ShutdownTimeout <= 0 {
		return errors.New("timeouts must be positive")
	}
	if config.ConnectionTimeout >= config.ShutdownTimeout {
		return errors.New("connection_timeout must fit inside shutdown_timeout so pending handshakes can finish")
	}
	if config.MaxConnections <= 0 || config.MaxRecvMessageSize <= 0 || config.MaxSendMessageSize <= 0 || config.MaxConcurrentStreams == 0 || config.MaxHeaderListSize == 0 {
		return errors.New("message, stream and header limits must be positive")
	}
	return nil
}
