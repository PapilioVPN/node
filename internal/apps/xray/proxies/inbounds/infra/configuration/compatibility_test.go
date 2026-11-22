package configuration_test

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestCredentialFreeCompatibilityAndDirection(t *testing.T) {
	tests := []struct {
		name, protocol, account, reason string
		live                            bool
	}{
		{"vless.inbound.Config", "vless", "xray.proxy.vless.Account", "supported", true},
		{"vmess.inbound.Config", "vmess", "xray.proxy.vmess.Account", "supported", true},
		{"trojan.ServerConfig", "trojan", "xray.proxy.trojan.Account", "supported", true},
		{"shadowsocks.ServerConfig", "shadowsocks", "xray.proxy.shadowsocks.Account", "supported", true},
		{"shadowsocks_2022.MultiUserServerConfig", "shadowsocks-2022", "xray.proxy.shadowsocks_2022.Account", "supported", true},
		{"hysteria.ServerConfig", "hysteria2", "xray.proxy.hysteria.account.Account", "supported", true},
		{"masque.ServerConfig", "masque", "xray.proxy.masque.Account", "supported", true},
		{"wireguard.DeviceConfig", "wireguard", "xray.proxy.wireguard.PeerConfig", "supported", true},
		{"shadowsocks_2022.ServerConfig", "shadowsocks-2022", "", "no_user_manager", false},
		{"http.ServerConfig", "http", "", "no_user_manager", false},
		{"socks.ServerConfig", "socks", "", "no_user_manager", false},
		{"vless.outbound.Config", "unknown", "", "unsupported_proxy_type", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Field 99 is opaque and must never appear in the safe projection.
			data, _ := proto.Marshal(&core.InboundHandlerConfig{Tag: "edge", ProxySettings: &serial.TypedMessage{Type: "xray.proxy." + tt.name, Value: []byte{0x9a, 6, 6, 's', 'e', 'c', 'r', 'e', 't'}}})
			summary, err := (configuration.Inspector{}).Describe(data)
			if err != nil || summary.Protocol != tt.protocol || summary.AccountType != tt.account || summary.Reason != tt.reason || summary.LiveAccounts != tt.live || summary.Direction != "inbound" {
				t.Fatalf("%+v %v", summary, err)
			}
		})
	}
	data, _ := proto.Marshal(&core.InboundHandlerConfig{ProxySettings: &serial.TypedMessage{Type: "xray.proxy.wireguard.DeviceConfig", Value: []byte{0x40, 1}}})
	summary, err := (configuration.Inspector{}).Describe(data)
	if err != nil || summary.LiveAccounts || summary.Reason != "client_mode" {
		t.Fatal(summary, err)
	}
}

func FuzzNativeCompatibility(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0xff})
	f.Add([]byte{0x40, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = (configuration.Inspector{}).Describe(data)
	})
}
