package settings

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

type Management struct {
	HTTPAddress             string `yaml:"http_address"`
	GRPCAddress             string `yaml:"grpc_address"`
	APIKey                  string `yaml:"api_key"`
	TLS                     TLS    `yaml:"tls"`
	RenewLeaseOnAuthRequest bool   `yaml:"renew_lease_on_auth_request"`
}

type TLS struct {
	CertFile     string `yaml:"cert_file"`
	KeyFile      string `yaml:"key_file"`
	ClientCAFile string `yaml:"client_ca_file"`
}

func (management *Management) validate(dir string) error {
	if err := validateAddress(management.HTTPAddress); err != nil {
		return fmt.Errorf("management.http_address: %w", err)
	}
	if err := validateAddress(management.GRPCAddress); err != nil {
		return fmt.Errorf("management.grpc_address: %w", err)
	}

	tls := &management.TLS
	if (tls.CertFile == "") != (tls.KeyFile == "") {
		return errors.New("management.tls requires both cert_file and key_file")
	}
	if tls.CertFile == "" && (management.APIKey != "" || tls.ClientCAFile != "") {
		return errors.New("management credentials require TLS")
	}
	if management.RenewLeaseOnAuthRequest && management.APIKey == "" && tls.ClientCAFile == "" {
		return errors.New("management.renew_lease_on_auth_request requires an API key or client certificate authentication")
	}
	tls.CertFile = resolve(dir, tls.CertFile)
	tls.KeyFile = resolve(dir, tls.KeyFile)
	tls.ClientCAFile = resolve(dir, tls.ClientCAFile)
	return nil
}

func (management Management) allowsUnauthenticatedRemote() bool {
	if management.APIKey != "" || management.TLS.ClientCAFile != "" {
		return false
	}
	for _, address := range []string{management.HTTPAddress, management.GRPCAddress} {
		host, _, _ := net.SplitHostPort(address)
		if !net.ParseIP(host).IsLoopback() {
			return true
		}
	}
	return false
}

func validateAddress(address string) error {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("expected a host and port")
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return errors.New("expected a numeric port from 0 to 65535")
	}
	return nil
}
