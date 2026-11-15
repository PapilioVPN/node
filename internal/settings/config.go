package settings

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// ErrInvalidConfig identifies invalid configuration without exposing YAML values.
var ErrInvalidConfig = errors.New("invalid configuration")

type Config struct {
	Node           Node           `yaml:"node"`
	Communications Communications `yaml:"communications"`
	Runtime        Runtime        `yaml:"runtime"`
	Instances      []Instance     `yaml:"instances"`
}

type Node struct {
	ID string `yaml:"id"`
}

type Communications struct {
	GRPC GRPC `yaml:"grpc"`
	HTTP HTTP `yaml:"http"`
	NATS NATS `yaml:"nats"`
}

type GRPC struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
	// RequestTimeout bounds unary RPCs; native health streams live until canceled.
	RequestTimeout       time.Duration   `yaml:"request_timeout"`
	ConnectionTimeout    time.Duration   `yaml:"connection_timeout"`
	MaxReceiveBytes      int             `yaml:"max_receive_bytes"`
	MaxConcurrentStreams uint32          `yaml:"max_concurrent_streams"`
	TLS                  TLS             `yaml:"tls"`
	Permissions          GRPCPermissions `yaml:"permissions"`
}

// GRPCPermissions grants only the declared operations to authenticated clients.
// Zero values preserve the foundation's node-info/health-only access.
type GRPCPermissions struct {
	ReadInstanceStatus     bool `yaml:"read_instance_status"`
	ControlInstances       bool `yaml:"control_instances"`
	ValidateInstanceConfig bool `yaml:"validate_instance_config"`
	ReadEngineInfo         bool `yaml:"read_engine_info"`
	ReadEngineStats        bool `yaml:"read_engine_stats"`
	ReadEngineOnline       bool `yaml:"read_engine_online"`
}

type TLS struct {
	CertificateFile string `yaml:"certificate_file"`
	PrivateKeyFile  string `yaml:"private_key_file"`
	ClientCAFile    string `yaml:"client_ca_file"`
}

type Runtime struct {
	StartupTimeout  time.Duration `yaml:"startup_timeout"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// Load reads Node settings without consulting environment variables.
func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	var config Config
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		// Native decoder errors can contain credential values as settings expand.
		return Config{}, fmt.Errorf("%w: invalid YAML, fields or value types", ErrInvalidConfig)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: exactly one YAML document is required", ErrInvalidConfig)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	location, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve configuration location: %w", err)
	}
	for _, tls := range []*TLS{&config.Communications.GRPC.TLS, &config.Communications.HTTP.TLS} {
		for _, value := range []*string{&tls.CertificateFile, &tls.PrivateKeyFile, &tls.ClientCAFile} {
			resolvePath(filepath.Dir(location), value)
		}
	}
	for _, value := range []*string{&config.Communications.NATS.TLS.CertificateFile, &config.Communications.NATS.TLS.PrivateKeyFile, &config.Communications.NATS.TLS.CAFile} {
		resolvePath(filepath.Dir(location), value)
	}
	for i := range config.Instances {
		xray := config.Instances[i].Xray
		resolvePath(filepath.Dir(location), &xray.Binary)
		resolvePath(filepath.Dir(location), &xray.ConfigFile)
		if api := xray.API; api != nil && api.Network == "unix" {
			resolvePath(filepath.Dir(location), &api.Address)
		}
		if validation := config.Instances[i].Validation; validation != nil {
			resolvePath(filepath.Dir(location), &validation.WorkDir)
		}
	}
	return config, nil
}

func resolvePath(base string, value *string) {
	if *value != "" && !filepath.IsAbs(*value) {
		*value = filepath.Join(base, *value)
	}
}

// Validate checks Node settings, not engine-native configuration contents.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Node.ID) == "" {
		return fmt.Errorf("%w: node.id is required", ErrInvalidConfig)
	}
	if c.Communications.GRPC.Enabled {
		_, port, err := net.SplitHostPort(c.Communications.GRPC.Listen)
		if err != nil {
			return fmt.Errorf("%w: communications.grpc.listen requires host:port", ErrInvalidConfig)
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 0 || number > 65535 {
			return fmt.Errorf("%w: communications.grpc.listen requires a port from 0 to 65535", ErrInvalidConfig)
		}
		grpc := c.Communications.GRPC
		if grpc.RequestTimeout <= 0 || grpc.ConnectionTimeout <= 0 || grpc.MaxReceiveBytes <= 0 || grpc.MaxConcurrentStreams == 0 {
			return fmt.Errorf("%w: positive gRPC timeouts and message/concurrency limits are required", ErrInvalidConfig)
		}
		if strings.TrimSpace(grpc.TLS.CertificateFile) == "" || strings.TrimSpace(grpc.TLS.PrivateKeyFile) == "" || strings.TrimSpace(grpc.TLS.ClientCAFile) == "" {
			return fmt.Errorf("%w: gRPC server certificate, private key and client CA files are required", ErrInvalidConfig)
		}
	}
	if err := c.Communications.HTTP.validate(); err != nil {
		return err
	}
	if err := c.Communications.NATS.validate(); err != nil {
		return err
	}
	if c.Communications.GRPC.Enabled || c.Communications.HTTP.Enabled || c.Communications.NATS.Enabled || len(c.Instances) > 0 {
		if c.Runtime.StartupTimeout <= 0 || c.Runtime.ShutdownTimeout <= 0 {
			return fmt.Errorf("%w: positive runtime startup/shutdown timeouts are required", ErrInvalidConfig)
		}
	}
	return validateInstances(c.Instances)
}

func validListen(address string) bool {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	value, err := strconv.Atoi(port)
	return err == nil && value >= 0 && value <= 65535
}
