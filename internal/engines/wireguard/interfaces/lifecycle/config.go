package lifecycle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"sync"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

const MaxConfigBytes = 1 << 20

type ConfigIssue uint8

// These codes match WireGuardConfigIssue in the v1 API.
const (
	ConfigIssueNone              ConfigIssue = 0
	ConfigIssueInvalidJSON       ConfigIssue = 1
	ConfigIssueInvalidPrivateKey ConfigIssue = 2
	ConfigIssueInvalidAddresses  ConfigIssue = 3
	ConfigIssueDuplicateAddress  ConfigIssue = 4
	ConfigIssueInvalidMTU        ConfigIssue = 5
	ConfigIssueInvalidListenPort ConfigIssue = 6
)

var configIssueCodes = [...]string{
	ConfigIssueNone:              "",
	ConfigIssueInvalidJSON:       "invalid_json",
	ConfigIssueInvalidPrivateKey: "invalid_private_key",
	ConfigIssueInvalidAddresses:  "invalid_addresses",
	ConfigIssueDuplicateAddress:  "duplicate_address",
	ConfigIssueInvalidMTU:        "invalid_mtu",
	ConfigIssueInvalidListenPort: "invalid_listen_port",
}

func (issue ConfigIssue) Error() string {
	if int(issue) >= len(configIssueCodes) {
		return ErrInvalid.Error()
	}
	return configIssueCodes[issue]
}

func (issue ConfigIssue) Unwrap() error { return ErrInvalid }

type Validator struct {
	name string
	mu   sync.Mutex
}

func NewValidator(name string) *Validator {
	return &Validator{name: name}
}

func (v *Validator) Check(ctx context.Context, name string, source []byte) (ConfigIssue, error) {
	if name == "" || name != v.name {
		return ConfigIssueNone, status.ErrNotFound
	}
	if len(source) > MaxConfigBytes {
		return ConfigIssueNone, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return ConfigIssueNone, err
	}
	if !v.mu.TryLock() {
		return ConfigIssueNone, status.ErrBusy
	}
	defer v.mu.Unlock()
	_, err := parseConfig(source)
	if canceled := ctx.Err(); canceled != nil {
		return ConfigIssueNone, canceled
	}
	var issue ConfigIssue
	if errors.As(err, &issue) {
		return issue, nil
	}
	return ConfigIssueNone, err
}

type Config struct {
	PrivateKey [32]byte
	Addresses  []netip.Prefix
	ListenPort uint16
	MTU        uint32
}

func parseConfig(source []byte) (Config, error) {
	if len(source) > MaxConfigBytes {
		return Config{}, ErrTooLarge
	}
	source = bytes.Trim(source, " \t\r\n")
	if len(source) == 0 || source[0] != '{' {
		return Config{}, ConfigIssueInvalidJSON
	}
	var input struct {
		PrivateKey []byte   `json:"private_key"`
		Addresses  []string `json:"addresses"`
		ListenPort uint16   `json:"listen_port"`
		MTU        uint32   `json:"mtu"`
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return Config{}, decodeIssue(err)
	}
	if decoder.Decode(new(json.RawMessage)) != io.EOF {
		return Config{}, ConfigIssueInvalidJSON
	}
	if len(input.PrivateKey) != 32 || [32]byte(input.PrivateKey) == [32]byte{} {
		return Config{}, ConfigIssueInvalidPrivateKey
	}
	if len(input.Addresses) == 0 || len(input.Addresses) > 16 {
		return Config{}, ConfigIssueInvalidAddresses
	}
	if input.MTU == 0 {
		input.MTU = 1420
	}
	if input.MTU < 576 || input.MTU > 9000 {
		return Config{}, ConfigIssueInvalidMTU
	}
	config := Config{
		PrivateKey: [32]byte(input.PrivateKey), ListenPort: input.ListenPort, MTU: input.MTU,
		Addresses: make([]netip.Prefix, 0, len(input.Addresses)),
	}
	for _, text := range input.Addresses {
		address, err := netip.ParsePrefix(text)
		if err != nil || address.Bits() == 0 || !address.Addr().IsGlobalUnicast() || address.Addr().Is4In6() {
			return Config{}, ConfigIssueInvalidAddresses
		}
		if address.Addr().Is6() && config.MTU < 1280 {
			return Config{}, ConfigIssueInvalidMTU
		}
		for _, existing := range config.Addresses {
			if existing.Addr() == address.Addr() {
				return Config{}, ConfigIssueDuplicateAddress
			}
		}
		config.Addresses = append(config.Addresses, address)
	}
	return config, nil
}

func decodeIssue(err error) ConfigIssue {
	var base64Err base64.CorruptInputError
	if errors.As(err, &base64Err) {
		return ConfigIssueInvalidPrivateKey
	}
	var field *json.UnmarshalTypeError
	if errors.As(err, &field) {
		switch field.Field {
		case "private_key":
			return ConfigIssueInvalidPrivateKey
		case "addresses":
			return ConfigIssueInvalidAddresses
		case "mtu":
			return ConfigIssueInvalidMTU
		case "listen_port":
			return ConfigIssueInvalidListenPort
		}
	}
	return ConfigIssueInvalidJSON
}
