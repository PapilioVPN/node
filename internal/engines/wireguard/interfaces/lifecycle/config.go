package lifecycle

import (
	"bytes"
	"encoding/json"
	"io"
	"net/netip"
)

const MaxConfigBytes = 1 << 20

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
	var input struct {
		PrivateKey []byte   `json:"private_key"`
		Addresses  []string `json:"addresses"`
		ListenPort uint16   `json:"listen_port"`
		MTU        uint32   `json:"mtu"`
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return Config{}, ErrInvalid
	}
	if len(input.PrivateKey) != 32 || [32]byte(input.PrivateKey) == [32]byte{} ||
		len(input.Addresses) == 0 || len(input.Addresses) > 16 {
		return Config{}, ErrInvalid
	}
	if input.MTU == 0 {
		input.MTU = 1420
	}
	if input.MTU < 576 || input.MTU > 9000 {
		return Config{}, ErrInvalid
	}
	config := Config{PrivateKey: [32]byte(input.PrivateKey), ListenPort: input.ListenPort, MTU: input.MTU}
	for _, text := range input.Addresses {
		address, err := netip.ParsePrefix(text)
		if err != nil || address.Bits() == 0 || !address.Addr().IsGlobalUnicast() || address.Addr().Is4In6() ||
			address.Addr().Is6() && config.MTU < 1280 {
			return Config{}, ErrInvalid
		}
		for _, existing := range config.Addresses {
			if existing.Addr() == address.Addr() {
				return Config{}, ErrInvalid
			}
		}
		config.Addresses = append(config.Addresses, address)
	}
	return config, nil
}
