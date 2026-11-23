package native

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type accountLayout struct {
	protocol    membership.Protocol
	accountType string
	usersField  int
}

var accountLayouts = map[string]accountLayout{
	vlessInboundType:                                    {membership.VLESS, vlessAccountType, 1},
	"xray.proxy.vmess.inbound.Config":                   {membership.VMess, "xray.proxy.vmess.Account", 1},
	"xray.proxy.trojan.ServerConfig":                    {membership.Trojan, "xray.proxy.trojan.Account", 1},
	"xray.proxy.shadowsocks.ServerConfig":               {membership.Shadowsocks, "xray.proxy.shadowsocks.Account", 1},
	"xray.proxy.shadowsocks_2022.MultiUserServerConfig": {membership.SS2022, "xray.proxy.shadowsocks_2022.Account", 3},
	"xray.proxy.hysteria.ServerConfig":                  {membership.Hysteria, "xray.proxy.hysteria.account.Account", 1},
	"xray.proxy.masque.ServerConfig":                    {membership.MASQUE, "xray.proxy.masque.Account", 1},
	"xray.proxy.wireguard.DeviceConfig":                 {membership.WireGuard, "xray.proxy.wireguard.PeerConfig", 5},
}

func (entry *inboundAccounts) name(email string) (string, error) {
	switch entry.layout.protocol {
	case membership.WireGuard, membership.Hysteria:
		return email, nil
	case membership.MASQUE:
		if strings.Contains(email, ":") {
			return "", membership.ErrInvalid
		}
	case membership.Shadowsocks:
		lower := strings.ToLower(email)
		if !strings.EqualFold(email, lower) {
			return "", membership.ErrInvalid
		}
		return strings.Map(func(r rune) rune {
			key := r
			for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
				key = min(key, next)
			}
			return key
		}, lower), nil
	}
	return membership.NameKey(email), nil
}

var shadowsocksCiphers = map[string]uint32{
	"aes-128-gcm": 5, "aead_aes_128_gcm": 5,
	"aes-256-gcm": 6, "aead_aes_256_gcm": 6,
	"chacha20-poly1305": 7, "chacha20-ietf-poly1305": 7, "aead_chacha20_poly1305": 7,
	"xchacha20-poly1305": 8, "aead_xchacha20_poly1305": 8,
}

func (entry *inboundAccounts) prepare(email string, c membership.Credential) (membership.PreparedAccount, error) {
	var account membership.PreparedAccount
	if c.Protocol != entry.layout.protocol {
		return account, membership.ErrProtocol
	}
	name, err := entry.name(email)
	if err != nil {
		return account, err
	}
	account.Email, account.Name = email, name

	var msg proto.Message
	switch c.Protocol {
	case membership.VLESS, membership.VMess:
		id, ok := membership.ParseUUID(c.UUID)
		if !ok {
			return account, membership.ErrInvalid
		}
		value := &VlessAccount{Id: id.String()}
		if c.Flow != nil {
			value.Flow = *c.Flow
		}
		msg = value
		account.KeepVersion = c.Protocol == membership.VLESS && c.Flow == nil
	case membership.Trojan, membership.MASQUE:
		msg = &PasswordAccount{Password: c.Password}
	case membership.Shadowsocks:
		cipher := shadowsocksCiphers[strings.ToLower(c.Method)]
		if cipher == 0 {
			return account, membership.ErrInvalid
		}
		msg = &ShadowsocksAccount{Password: c.Password, CipherType: cipher}
	case membership.SS2022:
		key, err := base64.StdEncoding.Strict().DecodeString(c.Key)
		if err != nil || len(key) != entry.ss2022KeySize {
			return account, membership.ErrInvalid
		}
		msg = &PasswordAccount{Password: base64.StdEncoding.EncodeToString(key)}
	case membership.Hysteria:
		msg = &PasswordAccount{Password: c.Auth}
	case membership.WireGuard:
		peer := &WireGuardAccount{PublicKey: c.Peer.PublicKey, PreSharedKey: c.Peer.PreSharedKey, AllowedIps: c.Peer.AllowedIPs}
		if c.Peer.KeepAlive != 0 {
			peer.KeepAlive = strconv.FormatUint(uint64(c.Peer.KeepAlive), 10)
		}
		normalized, err := normalizePeer(peer)
		if err != nil || len(normalized.AllowedIps) == 0 || normalized.PublicKey == entry.peerPublicKey {
			return account, membership.ErrInvalid
		}
		msg = normalized
	}
	account.Value, err = proto.Marshal(msg)
	if err != nil {
		return account, membership.ErrInvalid
	}
	account.Key, err = entry.key(name, account.Value)
	return account, err
}

func (entry *inboundAccounts) key(name string, wire []byte) (key membership.Key, err error) {
	invalid := membership.ErrInvalid
	switch entry.layout.protocol {
	case membership.VLESS, membership.VMess:
		var uuid VlessAccount
		if proto.Unmarshal(wire, &uuid) != nil {
			return key, invalid
		}
		id, ok := membership.ParseUUID(uuid.Id)
		if !ok {
			return key, invalid
		}
		key.Identity = sha256.Sum256(id[:])
		if entry.layout.protocol == membership.VLESS {
			key.Identity = id.Key()
			key.Version = sha256.Sum256([]byte(uuid.Flow))
		}
	case membership.Shadowsocks:
		var account ShadowsocksAccount
		if proto.Unmarshal(wire, &account) != nil || account.CipherType < 5 || account.CipherType > 8 {
			return key, invalid
		}
		value := append([]byte{byte(account.CipherType)}, account.Password...)
		key.Identity = sha256.Sum256(value)
	case membership.WireGuard:
		var account WireGuardAccount
		if proto.Unmarshal(wire, &account) != nil {
			return key, invalid
		}
		peer, err := normalizePeer(&account)
		if err != nil {
			return key, err
		}
		value, err := proto.Marshal(peer)
		if err != nil {
			return key, invalid
		}
		key.Identity = sha256.Sum256([]byte(peer.PublicKey))
		key.Version = sha256.Sum256(value)
	default:
		var account PasswordAccount
		if proto.Unmarshal(wire, &account) != nil {
			return key, invalid
		}
		key.Identity = sha256.Sum256([]byte(account.Password))
		switch entry.layout.protocol {
		case membership.MASQUE:
			key.Identity = sha256.Sum256([]byte(name))
			key.Version = sha256.Sum256([]byte(account.Password))
		case membership.Hysteria:
			// Hysteria's UUID index also masks bytes 6 and 7.
			if id, err := uuid.Parse(account.Password); err == nil {
				key.Identity = membership.UUID(id).Key()
				key.Version = sha256.Sum256([]byte(account.Password))
			}
		case membership.SS2022:
			raw, err := base64.StdEncoding.DecodeString(account.Password)
			if err != nil {
				raw = []byte(account.Password)
			}
			if len(raw) != entry.ss2022KeySize {
				return key, invalid
			}
			key.Identity = sha256.Sum256(raw)
		}
	}
	return key, nil
}

func normalizePeer(peer *WireGuardAccount) (*WireGuardAccount, error) {
	public, ok := wireGuardKey(peer.PublicKey)
	if !ok || public == strings.Repeat("0", 64) || len(peer.AllowedIps) > 32 {
		return nil, membership.ErrInvalid
	}
	var psk string
	if peer.PreSharedKey != "" {
		psk, ok = wireGuardKey(peer.PreSharedKey)
		if !ok {
			return nil, membership.ErrInvalid
		}
	}
	var keepAlive string
	if peer.KeepAlive != "" {
		n, err := strconv.ParseUint(peer.KeepAlive, 10, 16)
		if err != nil {
			return nil, membership.ErrInvalid
		}
		if n != 0 {
			keepAlive = strconv.FormatUint(n, 10)
		}
	}
	ips := make([]string, 0, len(peer.AllowedIps))
	for _, text := range peer.AllowedIps {
		prefix, err := netip.ParsePrefix(text)
		if err != nil || len(text) > 64 {
			return nil, membership.ErrInvalid
		}
		ips = append(ips, prefix.Masked().String())
	}
	slices.Sort(ips)
	return &WireGuardAccount{PublicKey: public, PreSharedKey: psk, KeepAlive: keepAlive, AllowedIps: slices.Compact(ips)}, nil
}

func wireGuardKey(text string) (string, bool) {
	if len(text) == 64 {
		_, err := hex.DecodeString(text)
		return strings.ToLower(text), err == nil
	}
	if len(text) == 44 {
		raw, err := base64.StdEncoding.Strict().DecodeString(text)
		return hex.EncodeToString(raw), err == nil && len(raw) == 32
	}
	return "", false
}
