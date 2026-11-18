package settings

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

type System struct {
	Host Host `yaml:"host"`
}

type Host struct {
	Identity *HostIdentity `yaml:"identity"`
}

type HostIdentity struct {
	AllowRead      bool              `yaml:"allow_read"`
	RequestTimeout time.Duration     `yaml:"request_timeout"`
	NATS           *NATSSubscription `yaml:"nats"`
}

func (config Config) validateSystem(remaining time.Duration) error {
	identity := config.System.Host.Identity
	if identity == nil {
		return nil
	}
	if identity.RequestTimeout <= 0 {
		return errors.New("system.host.identity.request_timeout must be positive")
	}
	if http := config.Communications.HTTP; http != nil && identity.RequestTimeout >= http.WriteTimeout {
		return errors.New("host identity request_timeout must fit inside HTTP write_timeout")
	}
	if nats := identity.NATS; nats != nil {
		if config.Communications.NATS == nil {
			return errors.New("host identity NATS endpoint requires communications.nats")
		}
		if nats.Subject == "" || strings.ContainsAny(nats.Subject, "*>") || strings.ContainsFunc(nats.Subject, unicode.IsSpace) {
			return errors.New("host identity NATS subject must be a nonempty literal subject")
		}
		for token := range strings.SplitSeq(nats.Subject, ".") {
			if token == "" {
				return errors.New("host identity NATS subject must not contain empty tokens")
			}
		}
		if nats.PendingMessages <= 0 || nats.PendingBytes <= 0 {
			return errors.New("host identity NATS pending limits must be positive")
		}
		if nats.ShutdownTimeout <= 0 {
			return errors.New("host identity NATS shutdown_timeout must be positive")
		}
		// The capability drains its subscription before the shared connection.
		remaining -= config.Communications.NATS.ShutdownTimeout
		if nats.ShutdownTimeout >= remaining {
			return errors.New("host identity NATS drain and transport budgets must fit inside shutdown_timeout")
		}
	}
	return nil
}
