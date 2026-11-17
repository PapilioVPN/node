package configuration

import (
	"errors"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	hysteria "github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/masque"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	ss2022 "github.com/xtls/xray-core/proxy/shadowsocks_2022"
	"github.com/xtls/xray-core/proxy/trojan"
	vless "github.com/xtls/xray-core/proxy/vless/inbound"
	vmess "github.com/xtls/xray-core/proxy/vmess/inbound"
	"github.com/xtls/xray-core/proxy/wireguard"
	"google.golang.org/protobuf/proto"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// Decode only selected user-capable native settings. Untargeted opaque payloads
// are never reconstructed. Unmarshal/marshal retain unknown fields in each typed
// configuration and its existing TypedMessage envelope.
func replaceUsers(settings *serial.TypedMessage, users []*protocol.User, accountType string) error {
	var message proto.Message
	var expectedType string
	switch settings.Type {
	case serial.GetMessageType(&vless.Config{}):
		config := new(vless.Config)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&vmess.Config{}):
		config := new(vmess.Config)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.User = users
		message = config
	case serial.GetMessageType(&trojan.ServerConfig{}):
		config := new(trojan.ServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&shadowsocks.ServerConfig{}):
		config := new(shadowsocks.ServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&ss2022.MultiUserServerConfig{}):
		config := new(ss2022.MultiUserServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&ss2022.ServerConfig{}):
		config := new(ss2022.ServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		// Native JSON building chooses single-user mode for zero clients. Convert
		// AES explicitly to the native multi-user message, even for an empty set.
		// Future unknown single-user fields cannot safely map to a different type.
		method, err := ss2022.GetCipherMethod(config.Method)
		if err != nil || method.IsChaCha || len(config.ProtoReflect().GetUnknown()) > 0 {
			return domain.ErrUserManagementUnsupported
		}
		message = &ss2022.MultiUserServerConfig{Method: config.Method, Key: config.Key, Network: config.Network, Users: users}
		expectedType = serial.GetMessageType(message)
	case serial.GetMessageType(&hysteria.ServerConfig{}):
		config := new(hysteria.ServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&masque.ServerConfig{}):
		config := new(masque.ServerConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		config.Users = users
		message = config
	case serial.GetMessageType(&wireguard.DeviceConfig{}):
		config := new(wireguard.DeviceConfig)
		if err := decode(settings, config); err != nil {
			return err
		}
		if config.IsClient {
			return domain.ErrUserManagementUnsupported
		}
		config.Users = users
		message = config
	default:
		return domain.ErrUserManagementUnsupported
	}
	if expectedType == "" {
		expectedType = settings.Type
	}
	if accountType != "" && accountType != expectedType {
		return domain.ErrInboundProtocolMismatch
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return errors.Join(domain.ErrInvalidUserConfiguration, err)
	}
	settings.Type, settings.Value = expectedType, payload
	return nil
}

func decode(settings *serial.TypedMessage, message proto.Message) error {
	if err := proto.Unmarshal(settings.Value, message); err != nil {
		return errors.Join(domain.ErrInvalidUserConfiguration, err)
	}
	return nil
}
