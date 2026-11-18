package settings

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

// Requests configures common execution bounds once for installed capabilities.
type Requests struct {
	Timeout time.Duration `yaml:"timeout"`
	NATS    *NATSRequests `yaml:"nats"`
}

// NATSRequests applies these limits independently to each installed subscription.
type NATSRequests struct {
	SubjectPrefix   string        `yaml:"subject_prefix"`
	PendingMessages int           `yaml:"pending_messages"`
	PendingBytes    int           `yaml:"pending_bytes"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

func (config Requests) validate(communications Communications) error {
	if config.Timeout <= 0 {
		return errors.New("communications.requests.timeout must be positive")
	}
	if http := communications.HTTP; http != nil && config.Timeout >= http.WriteTimeout {
		return errors.New("request timeout must fit inside HTTP write_timeout")
	}
	if nats := config.NATS; nats != nil {
		if communications.NATS == nil {
			return errors.New("NATS requests require communications.nats")
		}
		prefix := nats.SubjectPrefix
		if prefix == "" || strings.ContainsAny(prefix, "*>") || strings.ContainsFunc(prefix, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return errors.New("NATS request subject_prefix must be a nonempty literal subject")
		}
		for token := range strings.SplitSeq(prefix, ".") {
			if token == "" {
				return errors.New("NATS request subject_prefix must not contain empty tokens")
			}
		}
		if nats.PendingMessages <= 0 || nats.PendingBytes <= 0 {
			return errors.New("NATS request pending limits must be positive")
		}
		if nats.ShutdownTimeout <= 0 {
			return errors.New("NATS request shutdown_timeout must be positive")
		}
	}
	return nil
}
