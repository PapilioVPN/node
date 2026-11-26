package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PapilioVPN/node/internal/engines/hysteria/internal/fault"
	stats "github.com/PapilioVPN/node/internal/engines/hysteria/stats/observations"
)

// OpenOwned checks the child owns the bound listener before sending the
// native secret. Subsequent fresh connections are fenced by the same inode.
func OpenOwned(ctx context.Context, pid int, address netip.AddrPort, secret string) (*Client, error) {
	socket, ok := findListener(pid, address)
	if !ok {
		return nil, stats.ErrUnavailable
	}
	client := newStatsClient(address.String(), secret, socket)
	if err := client.probe(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

type ownedSocket struct{ fd, link string }

func (s ownedSocket) present() bool {
	link, err := os.Readlink(s.fd)
	return err == nil && link == s.link
}

func findListener(pid int, address netip.AddrPort) (ownedSocket, bool) {
	dir := "/proc/" + strconv.Itoa(pid)
	table, err := os.Open(dir + "/net/tcp")
	if err != nil {
		return ownedSocket{}, false
	}
	defer table.Close()
	ip := address.Addr().As4()
	target := fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(ip[:]), address.Port())
	scan := bufio.NewScanner(io.LimitReader(table, 8<<20))
	var inode string
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) > 9 && f[1] == target && f[3] == "0A" {
			inode = f[9]
			break
		}
	}
	if inode == "" {
		return ownedSocket{}, false
	}
	want := "socket:[" + inode + "]"
	fds, err := os.Open(dir + "/fd")
	if err != nil {
		return ownedSocket{}, false
	}
	defer fds.Close()
	for remaining := 8192; remaining > 0; remaining -= 64 {
		names, err := fds.Readdirnames(64)
		for _, name := range names {
			s := ownedSocket{fd: filepath.Join(dir, "fd", name), link: want}
			if s.present() {
				return s, true
			}
		}
		if err != nil {
			return ownedSocket{}, false
		}
	}
	return ownedSocket{}, false
}

type Client struct {
	base, secret string
	read, mutate *http.Client
}

func newStatsClient(address, secret string, socket ownedSocket) *Client {
	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if !socket.present() {
				return nil, fault.ErrUnavailable
			}
			conn, err := dialer.DialContext(ctx, "tcp4", address)
			if err != nil {
				return nil, err
			}
			if !socket.present() {
				_ = conn.Close()
				return nil, fault.ErrUnavailable
			}
			return conn, nil
		},
		MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 8,
		IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: time.Second,
		MaxResponseHeaderBytes: 8 << 10,
	}
	mutations := transport.Clone()
	// Clear is a mutating native GET. A fresh connection avoids net/http's
	// transparent retries on a reused connection after an ambiguous failure.
	mutations.DisableKeepAlives = true
	client := func(tr *http.Transport) *http.Client {
		return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Client{base: "http://" + address, secret: secret, read: client(transport), mutate: client(mutations)}
}

func (s *Client) Close() {
	s.read.CloseIdleConnections()
	s.mutate.CloseIdleConnections()
}

func (s *Client) probe(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	_, err := s.Online(ctx)
	return err
}

func (s *Client) Traffic(ctx context.Context, clear bool) (map[string]stats.Counter, error) {
	path := "/traffic"
	if clear {
		path += "?clear=1"
	}
	return nativeJSON[stats.Counter](ctx, s, path, clear)
}

func (s *Client) Online(ctx context.Context) (map[string]uint64, error) {
	return nativeJSON[uint64](ctx, s, "/online", false)
}

func (s *Client) Kick(ctx context.Context, ids []string) error {
	b, err := json.Marshal(ids)
	if err != nil {
		return stats.ErrInvalid
	}
	_, err = s.request(ctx, "POST", "/kick", bytes.NewReader(b), true, 0)
	return err
}

func nativeJSON[V any](ctx context.Context, s *Client, path string, mutation bool) (map[string]V, error) {
	data, err := s.request(ctx, "GET", path, nil, mutation, 1<<20)
	if err != nil {
		return nil, err
	}
	var rows map[string]V
	if json.Unmarshal(data, &rows) != nil || rows == nil {
		if mutation {
			return nil, stats.ErrEffectUnknown
		}
		return nil, stats.ErrUnavailable
	}
	return rows, nil
}

func (s *Client) request(ctx context.Context, method, path string, body io.Reader, mutation bool, max int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, body)
	if err != nil {
		return nil, stats.ErrUnavailable
	}
	req.Header.Set("Authorization", s.secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := s.read
	failure := stats.ErrUnavailable
	if mutation {
		client = s.mutate
		failure = stats.ErrEffectUnknown
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, failure
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, failure
	}
	if max == 0 {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, failure
	}
	if int64(len(data)) > max {
		if mutation {
			return nil, stats.ErrEffectUnknown
		}
		return nil, stats.ErrTooLarge
	}
	return data, nil
}
