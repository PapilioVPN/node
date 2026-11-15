package settings

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// NATS configures Core request/reply. The broker owns requester mTLS and subject ACLs.
type NATS struct {
	Enabled            bool          `yaml:"enabled"`
	URL                string        `yaml:"url"`
	SubjectPrefix      string        `yaml:"subject_prefix"`
	QueueGroup         string        `yaml:"queue_group"`
	RequestTimeout     time.Duration `yaml:"request_timeout"`
	ConnectTimeout     time.Duration `yaml:"connect_timeout"`
	DrainTimeout       time.Duration `yaml:"drain_timeout"`
	MaxRequestBytes    int           `yaml:"max_request_bytes"`
	MaxResponseBytes   int           `yaml:"max_response_bytes"`
	MaxPendingMessages int           `yaml:"max_pending_messages"`
	MaxPendingBytes    int           `yaml:"max_pending_bytes"`
	TLS                NATSClientTLS `yaml:"tls"`
}

type NATSClientTLS struct {
	CertificateFile string `yaml:"certificate_file"`
	PrivateKeyFile  string `yaml:"private_key_file"`
	CAFile          string `yaml:"ca_file"`
}

func (c NATS) validate() error {
	if !c.Enabled {
		return nil
	}
	endpoint, err := url.Parse(c.URL)
	if err != nil || endpoint.Scheme != "tls" || endpoint.User != nil || endpoint.Hostname() == "" || endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || !validListen(endpoint.Host) {
		return fmt.Errorf("%w: communications.nats.url requires one tls://host:port without credentials or extra components", ErrInvalidConfig)
	}
	port, _ := strconv.Atoi(endpoint.Port())
	if port == 0 {
		return fmt.Errorf("%w: NATS requires a positive broker port", ErrInvalidConfig)
	}
	if !validNATSSubject(c.SubjectPrefix) || !validNATSSubject(c.QueueGroup) {
		return fmt.Errorf("%w: NATS prefix/queue require nonempty subject tokens without wildcards or whitespace", ErrInvalidConfig)
	}
	if c.RequestTimeout <= 0 || c.ConnectTimeout <= 0 || c.DrainTimeout <= 0 || c.MaxRequestBytes <= 0 || c.MaxPendingMessages <= 0 || c.MaxPendingBytes < c.MaxRequestBytes || c.MaxResponseBytes < 256 {
		return fmt.Errorf("%w: positive NATS timeouts/limits, pending bytes >= request bytes and response bytes >= 256 are required", ErrInvalidConfig)
	}
	if strings.TrimSpace(c.TLS.CertificateFile) == "" || strings.TrimSpace(c.TLS.PrivateKeyFile) == "" || strings.TrimSpace(c.TLS.CAFile) == "" {
		return fmt.Errorf("%w: NATS client certificate, private key and broker CA files are required", ErrInvalidConfig)
	}
	return nil
}

func validNATSSubject(value string) bool {
	if strings.ContainsAny(value, "*> \t\r\n") {
		return false
	}
	for token := range strings.SplitSeq(value, ".") {
		if token == "" {
			return false
		}
	}
	return true
}
