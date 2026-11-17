package settings

import (
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/internal/shared/mtls"
)

type NATS struct {
	Servers           []string      `yaml:"servers"`
	TLS               mtls.Files    `yaml:"tls"`
	TLSHandshakeFirst bool          `yaml:"tls_handshake_first"`
	ConnectTimeout    time.Duration `yaml:"connect_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	ReconnectWait     time.Duration `yaml:"reconnect_wait"`
	ReconnectJitter   time.Duration `yaml:"reconnect_jitter"`
	// Native per-server attempt limit; zero disables reconnecting.
	MaxReconnects int `yaml:"max_reconnects"`
	// Native high-water mark: one protocol message may cross the threshold.
	ReconnectBufferSize int           `yaml:"reconnect_buffer_size"`
	PingInterval        time.Duration `yaml:"ping_interval"`
	MaxPingsOutstanding int           `yaml:"max_pings_outstanding"`
}

func (config NATS) validate() error {
	if len(config.Servers) == 0 {
		return errors.New("at least one broker is required")
	}
	for _, address := range config.Servers {
		broker, err := url.Parse(address)
		// Do not echo the URL: invalid input may contain credentials.
		if err != nil || broker.Scheme != "tls" || broker.Hostname() == "" || broker.User != nil || broker.Path != "" || broker.RawQuery != "" || broker.ForceQuery || broker.Fragment != "" || broker.Opaque != "" {
			return errors.New("servers require tls://host:port URLs without credentials, path, query or fragment")
		}
		port, err := strconv.ParseUint(broker.Port(), 10, 16)
		if err != nil || port == 0 {
			return errors.New("servers require a valid nonzero TCP port")
		}
	}
	if err := validateTLS(config.TLS); err != nil {
		return err
	}
	if config.ConnectTimeout <= 0 || config.ShutdownTimeout <= 0 || config.WriteTimeout <= 0 || config.ReconnectWait <= 0 || config.PingInterval <= 0 {
		return errors.New("timeouts and intervals must be positive")
	}
	if config.ReconnectJitter < 0 || config.MaxReconnects < 0 || config.MaxPingsOutstanding <= 0 {
		return errors.New("reconnect limits must be finite and ping capacity positive")
	}
	// Native NATS uses -1 to disable buffering; zero silently restores its default.
	if config.ReconnectBufferSize != -1 && config.ReconnectBufferSize <= 0 {
		return errors.New("reconnect_buffer_size must be positive or -1 to disable buffering")
	}
	return nil
}
