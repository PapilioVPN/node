package domain

type UserProtocol string

const (
	VLESS           UserProtocol = "vless"
	VMess           UserProtocol = "vmess"
	Trojan          UserProtocol = "trojan"
	Shadowsocks     UserProtocol = "shadowsocks"
	Shadowsocks2022 UserProtocol = "shadowsocks_2022"
	Hysteria        UserProtocol = "hysteria"
	MASQUE          UserProtocol = "masque"
	WireGuard       UserProtocol = "wireguard"
)

// UserAccount holds exactly one matching protocol record. Credentials are borrowed
// synchronously for one operation, never included in acknowledgements or logging.
type UserAccount struct {
	Protocol        UserProtocol
	VLESS           *VLESSAccount
	VMess           *VMessAccount
	Trojan          *PasswordAccount
	Shadowsocks     *ShadowsocksAccount
	Shadowsocks2022 *Shadowsocks2022Account
	Hysteria        *HysteriaAccount
	MASQUE          *PasswordAccount
	WireGuard       *WireGuardAccount
}

type VLESSAccount struct {
	ID   string
	Flow string
}
type VMessAccount struct{ ID string }
type PasswordAccount struct{ Password string }
type HysteriaAccount struct{ Auth string }

type ShadowsocksCipher string

const (
	AES128GCM         ShadowsocksCipher = "aes-128-gcm"
	AES256GCM         ShadowsocksCipher = "aes-256-gcm"
	ChaCha20Poly1305  ShadowsocksCipher = "chacha20-poly1305"
	XChaCha20Poly1305 ShadowsocksCipher = "xchacha20-poly1305"
	// CipherNone preserves the legacy wire value; engine versions may reject it.
	CipherNone ShadowsocksCipher = "none"
)

type ShadowsocksAccount struct {
	Password string
	Cipher   ShadowsocksCipher
	IVCheck  bool
}
type Shadowsocks2022Account struct{ Key []byte }
type WireGuardAccount struct {
	PublicKey        []byte
	PreSharedKey     []byte
	AllowedIPs       []string
	KeepAliveSeconds *uint32
}

type InboundUser struct {
	Email   string
	Level   uint32
	Account UserAccount
}
type InboundUserAddition struct {
	InboundTag string
	User       InboundUser
}
