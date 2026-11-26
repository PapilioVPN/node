// Package preflight reads and checks managed Hysteria server YAML without executing it.
package preflight

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	"go.yaml.in/yaml/v3"
)

type Source struct{ Path string }

const MaxBytes = 1 << 20

type Prepared struct {
	StatsAddress netip.AddrPort
	StatsSecret  string
}

type Issue string

const (
	InvalidYAML       Issue = "invalid_yaml"
	UnsupportedSource Issue = "unsupported_source"
	InvalidListener   Issue = "invalid_listener"
	InvalidStats      Issue = "invalid_traffic_stats"
	InvalidTLS        Issue = "invalid_tls"
	InvalidAuth       Issue = "invalid_auth"
)
const ValidationScope = "node_preflight"

func (b Source) Read(ctx context.Context, source []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil {
		var err error
		source, err = readFile(b.Path, MaxBytes)
		if err != nil {
			return nil, err
		}
	}
	if len(source) == 0 {
		return nil, fault.ErrInvalid
	}
	if len(source) > MaxBytes {
		return nil, fault.ErrTooLarge
	}
	return source, ctx.Err()
}

func readFile(path string, max int) ([]byte, error) {
	// Use the opened descriptor for the regular-file check. Nonblocking open
	// also covers a path replaced with a FIFO before the check.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fault.ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fault.ErrUnavailable
	}
	if info.Size() > int64(max) {
		return nil, fault.ErrTooLarge
	}
	result, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, fault.ErrUnavailable
	}
	if len(result) > max {
		return nil, fault.ErrTooLarge
	}
	return result, nil
}

type mapping map[string]*yaml.Node

func fields(n *yaml.Node) mapping {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	m := make(mapping, len(n.Content)/2)
	for i := 0; i < len(n.Content); i += 2 {
		m[strings.ToLower(n.Content[i].Value)] = n.Content[i+1]
	}
	return m
}

func (m mapping) text(key string) string {
	n := m[key]
	if n == nil || n.Kind != yaml.ScalarNode || n.ShortTag() != "!!str" {
		return ""
	}
	return n.Value
}

func (m mapping) stringField(key string) bool {
	n := m[key]
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!str"
}

func (m mapping) falseOrAbsent(key string) bool {
	n := m[key]
	return n == nil || n.Kind == yaml.ScalarNode && n.ShortTag() == "!!bool" && strings.EqualFold(n.Value, "false")
}

func safeTree(root *yaml.Node) bool {
	// Reject constructs whose Viper meaning could differ from the managed
	// projection. Do not expand aliases or merge keys.
	pending := []*yaml.Node{root}
	for len(pending) != 0 {
		last := len(pending) - 1
		n := pending[last]
		pending = pending[:last]
		switch n.Kind {
		case yaml.AliasNode:
			return false
		case yaml.MappingNode:
			seen := make(map[string]struct{}, len(n.Content)/2)
			for i := 0; i < len(n.Content); i += 2 {
				key := n.Content[i]
				name := strings.ToLower(key.Value)
				if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || name == "" {
					return false
				}
				if _, found := seen[name]; found {
					return false
				}
				seen[name] = struct{}{}
				pending = append(pending, n.Content[i+1])
			}
		case yaml.SequenceNode:
			pending = append(pending, n.Content...)
		}
	}
	return true
}

func (b Source) Check(ctx context.Context, source []byte) (Prepared, Issue, error) {
	if err := ctx.Err(); err != nil {
		return Prepared{}, "", err
	}
	reject := func(issue Issue) (Prepared, Issue, error) {
		return Prepared{}, issue, ctx.Err()
	}
	d := yaml.NewDecoder(bytes.NewReader(source))
	var doc, extra yaml.Node
	if d.Decode(&doc) != nil || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode || d.Decode(&extra) != io.EOF {
		return reject(InvalidYAML)
	}
	root := doc.Content[0]
	if !safeTree(root) {
		return reject(UnsupportedSource)
	}
	m := fields(root)
	auth := fields(m["auth"])
	authHTTP := fields(auth["http"])
	mimic, masq := fields(m["mimic"]), fields(m["masquerade"])
	for _, section := range []mapping{m, auth, authHTTP, mimic, masq, fields(m["trafficstats"]), fields(m["tls"])} {
		for key := range section {
			if strings.ContainsRune(key, '.') {
				return reject(UnsupportedSource)
			}
		}
	}
	if m["acme"] != nil || m["realm"] != nil || m["mimic"] != nil && (mimic == nil || !mimic.falseOrAbsent("enabled")) ||
		m["masquerade"] != nil && (masq == nil || !masq.stringField("type") || !masq.stringField("listenhttp") || !masq.stringField("listenhttps") ||
			strings.EqualFold(masq.text("type"), "file") || masq.text("listenhttp") != "" || masq.text("listenhttps") != "") {
		return reject(UnsupportedSource)
	}
	if !m.stringField("listen") || !singleUDP(m.text("listen")) {
		return reject(InvalidListener)
	}
	stats := fields(m["trafficstats"])
	address, err := netip.ParseAddrPort(stats.text("listen"))
	secret := stats.text("secret")
	if err != nil || address.Port() == 0 || !address.Addr().Is4() || !address.Addr().IsLoopback() ||
		strings.TrimSpace(secret) != secret || len(secret) < 1 || len(secret) > 512 ||
		strings.IndexFunc(secret, func(r rune) bool { return unicode.IsControl(r) }) >= 0 {
		return reject(InvalidStats)
	}
	if !validAuth(auth) {
		return reject(InvalidAuth)
	}
	if !checkTLS(filepath.Dir(b.Path), fields(m["tls"])) {
		return reject(InvalidTLS)
	}
	if err := ctx.Err(); err != nil {
		return Prepared{}, "", err
	}
	return Prepared{StatsAddress: address, StatsSecret: secret}, "", nil
}

func singleUDP(addr string) bool {
	if addr == "" {
		return true
	} // native default :443
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	value, err := strconv.ParseUint(port, 10, 16)
	if err != nil || value == 0 {
		return false
	}
	if host == "" {
		return true
	}
	_, err = netip.ParseAddr(host)
	return err == nil
}

func validAuth(auth mapping) bool {
	switch strings.ToLower(auth.text("type")) {
	case "password":
		return auth.text("password") != ""
	case "userpass":
		users := fields(auth["userpass"])
		for k := range users {
			if users.text(k) == "" {
				return false
			}
		}
		return len(users) > 0
	case "http", "https":
		settings := fields(auth["http"])
		u, err := url.Parse(settings.text("url"))
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || !settings.falseOrAbsent("insecure") {
			return false
		}
		if u.Scheme == "https" {
			return true
		}
		host, err := netip.ParseAddr(u.Hostname())
		return u.Scheme == "http" && err == nil && host.IsLoopback()
	default:
		return false
	}
}

func checkTLS(dir string, m mapping) bool {
	for _, key := range []string{"cert", "key", "sniguard", "clientca"} {
		if !m.stringField(key) {
			return false
		}
	}
	switch strings.ToLower(m.text("sniguard")) {
	case "", "dns-san", "strict", "disable":
	default:
		return false
	}
	cert, key := m.text("cert"), m.text("key")
	if cert == "" || key == "" {
		return false
	}
	read := func(path string) ([]byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		return readFile(path, 1<<20)
	}
	certData, err := read(cert)
	if err != nil {
		return false
	}
	keyData, err := read(key)
	if err != nil {
		return false
	}
	if _, err := tls.X509KeyPair(certData, keyData); err != nil {
		return false
	}
	if ca := m.text("clientca"); ca != "" {
		data, err := read(ca)
		if err != nil || !x509.NewCertPool().AppendCertsFromPEM(data) {
			return false
		}
	}
	return true
}
