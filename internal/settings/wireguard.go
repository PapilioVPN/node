package settings

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type WireGuard struct {
	Interface            string            `yaml:"interface"`
	Managed              bool              `yaml:"managed"`
	StateDirectory       string            `yaml:"state_directory"`
	PeerRemovalUsage     string            `yaml:"peer_removal_usage"`
	PeerPresharedKeyFile string            `yaml:"peer_preshared_key_file"`
	Probe                *WireGuardProbe   `yaml:"probe"`
	Routing              *WireGuardRouting `yaml:"routing"`
}

const WireGuardSampledPeerRemoval = "sampled"

type WireGuardRouting struct {
	Egress               string `yaml:"egress_interface"`
	EnableIPv4Forwarding bool   `yaml:"enable_ipv4_forwarding"`
	Masquerade           bool   `yaml:"masquerade"`
	Table                int    `yaml:"policy_table"`
	Priority             int    `yaml:"policy_priority"`
	Gateway              string `yaml:"gateway"`
}

type WireGuardProbe struct {
	URL                 string `yaml:"url"`
	Interface           string `yaml:"interface"`
	TimeoutMilliseconds uint32 `yaml:"timeout_ms"`
}

func (config WireGuard) Enabled() bool { return config.Interface != "" }

func (config *WireGuard) validate(dir string, management Management) error {
	switch config.PeerRemovalUsage {
	case "", "final", WireGuardSampledPeerRemoval:
	default:
		return errors.New("wireguard.peer_removal_usage must be final or sampled")
	}
	if config.PeerRemovalUsage != "" && !config.Managed {
		return errors.New("wireguard.peer_removal_usage requires managed mode")
	}
	if config.PeerPresharedKeyFile != "" {
		if !config.Managed || strings.ContainsRune(config.PeerPresharedKeyFile, 0) {
			return errors.New("wireguard.peer_preshared_key_file requires managed mode and a valid file path")
		}
		path, err := filepath.Abs(resolve(dir, config.PeerPresharedKeyFile))
		if err != nil {
			return errors.New("invalid wireguard.peer_preshared_key_file path")
		}
		config.PeerPresharedKeyFile = path
	}
	if r := config.Routing; r != nil {
		if !config.Managed || (r.Egress != "" && (!validWireGuardInterface(r.Egress) || r.Egress == config.Interface)) {
			return errors.New("wireguard.routing requires managed mode and a distinct Linux egress interface")
		}
		if r.Table == 0 {
			if r.Priority != 0 || r.Gateway != "" {
				return errors.New("wireguard.routing policy priority and gateway require a policy table")
			}
		} else {
			if r.Table < 1000 || r.Table > 2147483647 {
				return errors.New("wireguard.routing.policy_table must be between 1000 and 2147483647")
			}
			if r.Priority == 0 {
				r.Priority = 19000
			}
			if r.Priority < 1 || r.Priority >= 32766 {
				return errors.New("invalid wireguard routing policy priority")
			}
			if r.Gateway != "" {
				addr, err := netip.ParseAddr(r.Gateway)
				if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() {
					return errors.New("wireguard routing gateway must be an IPv4 unicast address")
				}
			}
		}
	}
	if probe := config.Probe; probe != nil {
		if !config.Managed {
			return errors.New("wireguard.probe requires managed mode")
		}
		target, err := url.Parse(probe.URL)
		if err != nil || target.Hostname() == "" || (target.Scheme != "http" && target.Scheme != "https") || target.User != nil || target.Fragment != "" {
			return errors.New("wireguard.probe.url must be an absolute HTTP or HTTPS URL without credentials or a fragment")
		}
		if probe.Interface == "" {
			probe.Interface = config.Interface
		}
		if !validWireGuardInterface(probe.Interface) {
			return errors.New("wireguard.probe.interface must be a valid Linux interface name")
		}
		if probe.TimeoutMilliseconds == 0 {
			probe.TimeoutMilliseconds = 1500
		}
		if probe.TimeoutMilliseconds > 5000 {
			return errors.New("wireguard.probe.timeout_ms exceeds 5000")
		}
	}
	if config.StateDirectory != "" {
		if !config.Managed {
			return errors.New("wireguard.state_directory requires managed mode")
		}
		if strings.ContainsRune(config.StateDirectory, 0) {
			return errors.New("wireguard.state_directory must not contain NUL")
		}
		path, err := filepath.Abs(resolve(dir, config.StateDirectory))
		if err != nil {
			return fmt.Errorf("resolve wireguard state directory: %w", err)
		}
		config.StateDirectory = path
	}
	if !config.Enabled() {
		if config.Managed {
			return errors.New("wireguard.interface is required when managed")
		}
		return nil
	}
	name := config.Interface
	if !validWireGuardInterface(name) {
		return errors.New("wireguard.interface must be a valid Linux interface name")
	}
	if management.allowsUnauthenticatedRemote() {
		return errors.New("remote wireguard management requires TLS with an API key or a client CA")
	}
	return nil
}

func validWireGuardInterface(name string) bool {
	return name != "" && len(name) < unix.IFNAMSIZ && utf8.ValidString(name) && name != "." && name != ".." &&
		!strings.ContainsAny(name, "\x00/: \t\n\v\f\r")
}
