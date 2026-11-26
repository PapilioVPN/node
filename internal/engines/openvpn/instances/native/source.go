package native

import (
	"bytes"
	"slices"
	"strings"

	"github.com/PapilioVPN/node/internal/engines/openvpn/instances/lifecycle"
)

// Managed server profiles permit data-plane and TLS options. Node owns the
// process, management connection and logging destination.
var directives = strings.Fields(
	"allow-compression auth auth-gen-token bind ca capath cert cipher client-to-client " +
		"compress comp-lzo connect-freq connect-freq-initial crl-verify data-ciphers data-ciphers-fallback " +
		"dev dev-type dh disable-dco duplicate-cn ecdh-curve explicit-exit-notify extra-certs fast-io " +
		"float fragment group hand-window ifconfig ifconfig-ipv6 ifconfig-ipv6-pool ifconfig-noexec ifconfig-pool " +
		"inactive keepalive key key-direction link-mtu local lport max-clients max-routes-per-client " +
		"mssfix mtu-disc net30 ncp-ciphers passtos persist-key persist-tun ping ping-exit ping-restart " +
		"port proto push rcvbuf reneg-bytes reneg-pkts reneg-sec replay-window route route-delay " +
		"route-gateway route-ipv6 route-metric route-noexec route-nopull server server-ipv6 session-timeout " +
		"sndbuf socket-flags tcp-nodelay tls-auth tls-cert-profile tls-cipher tls-ciphersuites tls-crypt " +
		"tls-crypt-v2 tls-groups tls-server tls-timeout tls-version-max tls-version-min topology " +
		"tran-window tun-mtu tun-mtu-extra txqueuelen user username-as-common-name verb verify-client-cert " +
		"verify-x509-name x509-username-field")
var inlineFiles = []string{"ca", "cert", "crl-verify", "dh", "extra-certs", "key", "tls-auth", "tls-crypt", "tls-crypt-v2"}

func checkSource(source []byte, managedRevocations bool) lifecycle.Issue {
	// Keep the framing below OpenVPN 2.6's fgets buffer limit in both the
	// outer options and inline reader. They must see the same line boundaries.
	if len(bytes.TrimSpace(source)) == 0 || bytes.IndexByte(source, 0) >= 0 {
		return lifecycle.Issue{Code: "invalid_source"}
	}
	var closing []byte
	var line uint32
	var hasDirective bool
	for len(source) > 0 {
		line++
		raw, rest, _ := bytes.Cut(source, []byte{'\n'})
		source = rest
		if len(raw) >= 255 {
			return lifecycle.Issue{Code: "invalid_source", Line: line}
		}
		if line == 1 {
			raw = bytes.TrimPrefix(raw, []byte{0xef, 0xbb, 0xbf})
		}
		value := bytes.Trim(raw, " \t\v\f\r")
		if closing != nil {
			if bytes.HasPrefix(value, closing) {
				if !bytes.Equal(value, closing) {
					return lifecycle.Issue{Code: "invalid_source", Line: line}
				}
				closing = nil
			}
			continue
		}
		if len(value) == 0 || value[0] == '#' || value[0] == ';' {
			continue
		}
		if value[0] == '<' && value[len(value)-1] == '>' && slices.Contains(inlineFiles, string(value[1:len(value)-1])) {
			if managedRevocations && (bytes.Equal(value, []byte("<ca>")) || bytes.Equal(value, []byte("<crl-verify>"))) {
				return lifecycle.Issue{Code: "unsupported_directive", Line: line}
			}
			closing = append([]byte("</"), value[1:]...)
			hasDirective = true
			continue
		}
		first := value
		var args []byte
		if i := bytes.IndexAny(value, " \t\v\f\r"); i >= 0 {
			first = value[:i]
			args = bytes.TrimSpace(value[i:])
		}
		first = bytes.TrimPrefix(first, []byte("--"))
		if !slices.Contains(directives, string(first)) ||
			managedRevocations && slices.Contains([]string{"ca", "capath", "crl-verify"}, string(first)) {
			return lifecycle.Issue{Code: "unsupported_directive", Line: line}
		}
		// The managed profile uses client certificates. A future external
		// authentication policy can own other verification modes.
		if bytes.Equal(first, []byte("verify-client-cert")) && !bytes.Equal(args, []byte("require")) {
			return lifecycle.Issue{Code: "unsupported_directive", Line: line}
		}
		hasDirective = true
	}
	if closing != nil || !hasDirective {
		return lifecycle.Issue{Code: "invalid_source", Line: line}
	}
	return lifecycle.Issue{}
}
