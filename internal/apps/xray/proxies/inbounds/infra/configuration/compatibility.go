package configuration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"github.com/xtls/xray-core/app/proxyman"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// Inspector projects only native type and account-manager structure. It imports
// no proxy servers. Layouts follow the pinned native config and UserManager ABI.
type Inspector struct{}

func (Inspector) Describe(content []byte) (domain.Summary, error) {
	var config core.InboundHandlerConfig
	if err := proto.Unmarshal(content, &config); err != nil {
		return domain.Summary{}, domain.ErrInvalidArgument
	}
	return (Inspector{}).DescribeNative(&config)
}

// DescribeNative avoids serializing and reparsing each whole native record when
// producing a lightweight projection from the SDK's already-decoded response.
func (Inspector) DescribeNative(config *core.InboundHandlerConfig) (domain.Summary, error) {
	if config == nil || config.ProxySettings == nil {
		return domain.Summary{}, domain.ErrInvalidArgument
	}
	summary := domain.Summary{Tag: config.Tag, Direction: "inbound", NativeType: config.ProxySettings.Type, Protocol: "unknown", Reason: "no_user_manager"}
	switch config.ProxySettings.Type {
	case "xray.proxy.vless.inbound.Config":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "vless", "xray.proxy.vless.Account", "lowercase"
	case "xray.proxy.vmess.inbound.Config":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "vmess", "xray.proxy.vmess.Account", "lowercase"
	case "xray.proxy.trojan.ServerConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "trojan", "xray.proxy.trojan.Account", "lowercase"
	case "xray.proxy.shadowsocks.ServerConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "shadowsocks", "xray.proxy.shadowsocks.Account", "native_equal_fold"
	case "xray.proxy.shadowsocks_2022.MultiUserServerConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "shadowsocks-2022", "xray.proxy.shadowsocks_2022.Account", "lowercase"
	case "xray.proxy.shadowsocks_2022.ServerConfig", "xray.proxy.shadowsocks_2022.RelayServerConfig":
		summary.Protocol = "shadowsocks-2022"
	case "xray.proxy.wireguard.DeviceConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "wireguard", "xray.proxy.wireguard.PeerConfig", "exact"
	case "xray.proxy.hysteria.ServerConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "hysteria2", "xray.proxy.hysteria.account.Account", "exact"
	case "xray.proxy.masque.ServerConfig":
		summary.Protocol, summary.AccountType, summary.EmailMatching = "masque", "xray.proxy.masque.Account", "lowercase"
	case "xray.proxy.http.ServerConfig":
		summary.Protocol = "http"
	case "xray.proxy.socks.ServerConfig":
		summary.Protocol = "socks"
	case "xray.proxy.dokodemo.Config":
		summary.Protocol = "tunnel"
	case "xray.proxy.tun.Config":
		summary.Protocol = "tun"
	default:
		summary.Reason = "unsupported_proxy_type"
	}
	// Check protobuf framing without decoding account/credential fields. WireGuard
	// DeviceConfig is also used by the outbound client: type name alone is unsafe.
	client := false
	for data := config.ProxySettings.Value; len(data) > 0; {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return domain.Summary{}, domain.ErrInvalidArgument
		}
		data = data[n:]
		if config.ProxySettings.Type == "xray.proxy.wireguard.DeviceConfig" && number == 8 {
			if kind != protowire.VarintType {
				return domain.Summary{}, domain.ErrInvalidArgument
			}
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return domain.Summary{}, domain.ErrInvalidArgument
			}
			client = value != 0
		}
		n = protowire.ConsumeFieldValue(number, kind, data)
		if n < 0 {
			return domain.Summary{}, domain.ErrInvalidArgument
		}
		data = data[n:]
	}
	if client {
		summary.AccountType, summary.EmailMatching, summary.Reason = "", "", "client_mode"
	}
	if summary.AccountType != "" {
		summary.LiveAccounts, summary.Reason = true, "supported"
	}
	return summary, nil
}

// Fingerprint uses deterministic native encoding for plan comparisons. It is
// private equality evidence, not an account-membership or desired-state revision.
func (Inspector) Fingerprint(content []byte) (string, error) {
	var config core.InboundHandlerConfig
	if err := proto.Unmarshal(content, &config); err != nil {
		return "", errors.Join(domain.ErrInvalidArgument, err)
	}
	if config.ReceiverSettings == nil || config.ProxySettings == nil || config.ProxySettings.Type == "" || config.ReceiverSettings.Type == "" {
		return "", domain.ErrInvalidArgument
	}
	return fingerprint(&config)
}
func fingerprint(config *core.InboundHandlerConfig) (string, error) {
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(config)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Candidate checks native receiver framing and selected User account types before
// an irreversible removal. It is structural preflight, not a constructor, bind,
// credential-semantic validation or engine readiness guarantee.
func (i Inspector) Candidate(content []byte) (domain.Summary, string, error) {
	var config core.InboundHandlerConfig
	if err := proto.Unmarshal(content, &config); err != nil {
		return domain.Summary{}, "", domain.ErrInvalidArgument
	}
	if config.ReceiverSettings == nil || config.ReceiverSettings.Type != "xray.app.proxyman.ReceiverConfig" || config.ProxySettings == nil || config.ProxySettings.Type == "" {
		return domain.Summary{}, "", domain.ErrInvalidArgument
	}
	var receiver proxyman.ReceiverConfig
	if err := proto.Unmarshal(config.ReceiverSettings.Value, &receiver); err != nil {
		return domain.Summary{}, "", domain.ErrInvalidArgument
	}
	summary, err := i.DescribeNative(&config)
	if err != nil {
		return domain.Summary{}, "", err
	}
	if summary.LiveAccounts {
		field := protowire.Number(1)
		switch summary.NativeType {
		case "xray.proxy.shadowsocks_2022.MultiUserServerConfig":
			field = 3
		case "xray.proxy.wireguard.DeviceConfig":
			field = 5
		}
		for data := config.ProxySettings.Value; len(data) > 0; {
			number, kind, n := protowire.ConsumeTag(data)
			if n < 0 {
				return domain.Summary{}, "", domain.ErrInvalidArgument
			}
			data = data[n:]
			size := protowire.ConsumeFieldValue(number, kind, data)
			if size < 0 {
				return domain.Summary{}, "", domain.ErrInvalidArgument
			}
			if number == field {
				if kind != protowire.BytesType {
					return domain.Summary{}, "", domain.ErrInvalidArgument
				}
				raw, n := protowire.ConsumeBytes(data)
				if n < 0 {
					return domain.Summary{}, "", domain.ErrInvalidArgument
				}
				var user protocol.User
				if err := proto.Unmarshal(raw, &user); err != nil || user.Account == nil || user.Account.Type != summary.AccountType {
					return domain.Summary{}, "", domain.ErrInvalidArgument
				}
			}
			data = data[size:]
		}
	}
	hash, err := fingerprint(&config)
	return summary, hash, err
}
