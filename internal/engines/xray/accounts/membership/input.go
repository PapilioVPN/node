package membership

// Protocol selects the credential accepted by the target native inbound.
type Protocol string

const (
	VLESS       Protocol = "vless"
	VMess       Protocol = "vmess"
	Trojan      Protocol = "trojan"
	Shadowsocks Protocol = "shadowsocks"
	SS2022      Protocol = "shadowsocks2022"
	Hysteria    Protocol = "hysteria"
	MASQUE      Protocol = "masque"
	WireGuard   Protocol = "wireguard"
)

type Peer struct {
	PublicKey    string   `json:"public_key"`
	PreSharedKey string   `json:"pre_shared_key,omitempty"`
	AllowedIPs   []string `json:"allowed_ips"`
	KeepAlive    uint16   `json:"keep_alive,omitempty"`
}

// Credential uses the protocol's fields only. A WireGuard peer carries public
// material and its optional PSK, never a private key.
type Credential struct {
	Protocol Protocol `json:"protocol"`
	UUID     string   `json:"uuid,omitempty"`
	Flow     *string  `json:"flow,omitempty"`
	Password string   `json:"password,omitempty"`
	Method   string   `json:"method,omitempty"`
	Key      string   `json:"key,omitempty"`
	Auth     string   `json:"auth,omitempty"`
	Peer     *Peer    `json:"peer,omitempty"`
}

type AccountInput struct {
	Email      string
	UUID       *string
	Flow       *string
	Credential *Credential
}

func (a AccountInput) selected() (*Credential, error) {
	switch {
	case !validName(a.Email, 1024):
		return nil, ErrInvalid
	case a.Credential != nil:
		if a.UUID != nil || a.Flow != nil || !a.Credential.valid() {
			return nil, ErrInvalid
		}
		return a.Credential, nil
	case a.UUID != nil:
		c := &Credential{Protocol: VLESS, UUID: *a.UUID, Flow: a.Flow}
		if !c.valid() {
			return nil, ErrInvalid
		}
		return c, nil
	case a.Flow != nil:
		return nil, ErrInvalid
	default:
		return nil, nil
	}
}

func (c Credential) valid() bool {
	switch c.Protocol {
	case VLESS, VMess:
		_, valid := ParseUUID(c.UUID)
		return valid && c.Password == "" && c.Method == "" && c.Key == "" && c.Auth == "" && c.Peer == nil &&
			(c.Flow == nil || (c.Protocol == VLESS && (*c.Flow == "" || *c.Flow == "xtls-rprx-vision")))
	case Trojan, MASQUE, Shadowsocks:
		return validName(c.Password, 1024) && c.UUID == "" && c.Flow == nil && c.Key == "" && c.Auth == "" && c.Peer == nil &&
			((c.Protocol == Shadowsocks) == (c.Method != ""))
	case SS2022:
		return validName(c.Key, 64) && c.UUID == "" && c.Flow == nil && c.Password == "" && c.Method == "" && c.Auth == "" && c.Peer == nil
	case Hysteria:
		return validName(c.Auth, 1024) && c.UUID == "" && c.Flow == nil && c.Password == "" && c.Method == "" && c.Key == "" && c.Peer == nil
	case WireGuard:
		return c.Peer != nil && c.UUID == "" && c.Flow == nil && c.Password == "" && c.Method == "" && c.Key == "" && c.Auth == ""
	default:
		return false
	}
}
