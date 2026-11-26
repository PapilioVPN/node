package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/engines/hysteria/instances/bridge/grpc/proto/v1"
	logsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/logs/bridge/grpc/proto/v1"
	statsv1 "github.com/PapilioVPN/node/internal/engines/hysteria/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

const (
	user       = "fixture-core-account"
	otherUser  = "fixture-core-other-account"
	otherToken = "fixture-second-user-token"
)

type observation struct {
	TX uint64 `json:"tx_bytes,string"`
	RX uint64 `json:"rx_bytes,string"`
}

func TestNativeTrafficAndCoreAuth(t *testing.T) {
	for _, transport := range []string{"HTTP", "GRPC"} {
		t.Run(transport, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 14*time.Second)
			defer cancel()
			var allowed atomic.Bool
			allowed.Store(true)
			var accepted, rejected, invalid atomic.Int32
			core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Addr string  `json:"addr"`
					Auth string  `json:"auth"`
					TX   *uint64 `json:"tx"`
				}
				decode := json.NewDecoder(r.Body)
				decode.DisallowUnknownFields()
				if r.Method != http.MethodPost || r.URL.Path != "/internal/hysteria-auth" || r.Header.Get("Content-Type") != "application/json" || decode.Decode(&req) != nil || req.TX == nil {
					invalid.Add(1)
					http.Error(w, "invalid callback", 400)
					return
				}
				if _, _, err := net.SplitHostPort(req.Addr); err != nil {
					invalid.Add(1)
					http.Error(w, "invalid address", 400)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if (req.Auth != password && req.Auth != otherToken) || !allowed.Load() {
					rejected.Add(1)
					_, _ = io.WriteString(w, `{"ok":false,"id":""}`)
					return
				}
				accepted.Add(1)
				id := user
				if req.Auth == otherToken {
					id = otherUser
				}
				_, _ = io.WriteString(w, `{"ok":true,"id":"`+id+`"}`)
			}))
			t.Cleanup(core.Close)
			f := setup(t, "  type: http\n  http:\n    url: "+core.URL+"/internal/hysteria-auth\n")
			api := observe(t, ctx, f, transport)
			defer api.stop()
			events := api.logs()
			target := echoTarget(t)
			f.rejectedClient(t, "wrong-fixture-token", target)
			if rejected.Load() == 0 {
				t.Fatalf("Core did not reject authentication (%d malformed callback requests)", invalid.Load())
			}
			first := f.client(t, password, target)
			defer first.close()
			a, b := first.dial(t), first.dial(t)
			defer a.Close()
			defer b.Close()
			echo(t, a, "payload-alpha")
			echo(t, b, "payload-beta")
			if got := api.online(); got != 1 {
				t.Fatalf("two streams on one client counted as %d devices", got)
			}
			second := f.client(t, password, target)
			if got := api.online(); got != 2 {
				t.Fatalf("two clients counted as %d devices", got)
			}
			second.close()
			until(t, func() bool { return api.online() == 1 })
			if accepted.Load() < 2 {
				t.Fatal("Core did not return the stable account ID")
			}
			other := f.client(t, otherToken, target)
			echo(t, other.dial(t), "traffic for a separate identity")
			other.close()
			before := api.traffic()
			if before.TX == 0 || before.RX != 2*before.TX {
				t.Fatalf("native TX/RX did not match client upload/download: %+v", before)
			}
			api.reset(before)
			echo(t, a, "new counters")
			if after := api.traffic(); after.TX == 0 || after.RX != 2*after.TX || after.TX >= before.TX {
				t.Fatalf("counter did not reset: %+v", after)
			}
			select {
			case event := <-events:
				if !bytes.Contains(event, []byte("client_connected")) || bytes.Contains(event, []byte(password)) || bytes.Contains(event, []byte(statsSecret)) || bytes.Contains(event, []byte(user)) {
					t.Fatal("unexpected or unredacted log event")
				}
			case <-ctx.Done():
				t.Fatal("no native connection event received")
			}
			api.kick()
			// The upstream kick is consumed by the next accounted traffic
			// event. A transport acknowledgement alone does not prove it.
			_ = a.SetDeadline(time.Now().Add(2 * time.Second))
			_, _ = a.Write([]byte("trigger kick"))
			_, err := a.Read(make([]byte, 32))
			if err == nil {
				t.Fatal("traffic passed after kick")
			}
			until(t, func() bool { return api.online() == 0 })
			first.close()
			again := f.client(t, password, target)
			echo(t, again.dial(t), "allowed to reconnect")
			if got := api.online(); got != 1 {
				t.Fatalf("kick acted as a ban: %d online", got)
			}
			again.close()
			allowed.Store(false)
			f.rejectedClient(t, password, target)
			if rejected.Load() < 2 {
				t.Fatal("Core policy did not independently block reconnect")
			}
			allowed.Store(true)
			active := f.client(t, password, target)
			defer active.close()
			activeFlow := active.dial(t)
			echo(t, activeFlow, "traffic immediately before stop")
			api.stop()
			assertClean(t, f)
			_ = activeFlow.SetDeadline(time.Now().Add(500 * time.Millisecond))
			_, _ = activeFlow.Write([]byte("after stop"))
			if _, err := activeFlow.Read(make([]byte, 32)); err == nil {
				t.Fatal("traffic passed through a stopped server")
			}
		})
	}
}

type observations struct {
	online  func() uint64
	traffic func() observation
	reset   func(observation)
	kick    func()
	logs    func() <-chan []byte
	stop    func()
}

func observe(t *testing.T, ctx context.Context, f *fixture, transport string) observations {
	t.Helper()
	if transport == "HTTP" {
		f.request(t, http.MethodPut, "/execution", nil, 200)
		f.request(t, http.MethodGet, "/stats/runtime", nil, 200)
		return observations{
			online: func() uint64 {
				var r struct {
					Users []struct {
						ID      string `json:"user_id"`
						Devices uint64 `json:"devices,string"`
					}
				}
				decodeJSON(t, f.request(t, http.MethodGet, "/stats/online/users", nil, 200), &r)
				for _, u := range r.Users {
					if u.ID == user {
						return u.Devices
					}
				}
				return 0
			},
			traffic: func() observation {
				var o observation
				decodeJSON(t, f.request(t, http.MethodGet, "/stats/traffic?user_id="+user, nil, 200), &o)
				return o
			},
			reset: func(want observation) {
				var r struct {
					Entries []struct {
						ID string `json:"user_id"`
						observation
					}
					Scope string `json:"reset_scope"`
				}
				decodeJSON(t, f.request(t, http.MethodPost, "/stats/traffic/resets", nil, 200), &r)
				if r.Scope != "instance" || len(r.Entries) != 2 || r.Entries[0].ID != user || r.Entries[0].observation != want || r.Entries[1].ID != otherUser || r.Entries[1].TX == 0 || r.Entries[1].RX == 0 {
					t.Fatalf("reset snapshot: %+v", r)
				}
				f.request(t, http.MethodGet, "/stats/traffic?user_id="+user, nil, 404)
				f.request(t, http.MethodGet, "/stats/traffic?user_id="+otherUser, nil, 404)
				f.request(t, http.MethodPost, "/stats/traffic/resets?user_id="+user, nil, 400)
			},
			kick: func() {
				f.request(t, http.MethodPost, "/stats/online/disconnections", []byte(`{"user_ids":["`+user+`"]}`), 200)
			},
			logs: func() <-chan []byte { return f.httpLogs(t, ctx) },
			stop: sync.OnceFunc(func() { f.request(t, http.MethodDelete, "/execution", nil, 200) }),
		}
	}
	conn := f.grpcConn(t)
	instances := v1.NewHysteriaInstancesServiceClient(conn)
	stats := statsv1.NewHysteriaStatsServiceClient(conn)
	id := &statsv1.HysteriaStatsRequest{Id: "edge"}
	if _, err := instances.Start(ctx, &v1.StartHysteriaRequest{Id: "edge"}); err != nil {
		t.Fatal(err)
	}
	runtime, err := stats.GetRuntimeStats(ctx, id)
	if err != nil || runtime.GetExecution() != 1 || runtime.GetReadySinceUnixNano() <= 0 {
		t.Fatalf("runtime: %v", err)
	}
	return observations{
		online: func() uint64 {
			r, err := stats.ListOnlineUsers(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range r.GetUsers() {
				if u.GetUserId() == user {
					return u.GetDevices()
				}
			}
			return 0
		},
		traffic: func() observation {
			r, err := stats.GetTraffic(ctx, &statsv1.HysteriaTrafficRequest{Id: "edge", UserId: user})
			if err != nil {
				t.Fatal(err)
			}
			return observation{r.GetTxBytes(), r.GetRxBytes()}
		},
		reset: func(want observation) {
			raw, err := stats.ListTraffic(ctx, id)
			if err != nil || len(raw.GetEntries()) != 2 {
				t.Fatalf("traffic list: %v", err)
			}
			r, err := stats.ResetTraffic(ctx, id)
			if err != nil || r.GetResetScope() != "instance" || len(r.GetEntries()) != 2 || r.Entries[0].GetUserId() != user || r.Entries[1].GetUserId() != otherUser || r.Entries[1].GetTxBytes() == 0 || r.Entries[1].GetRxBytes() == 0 {
				t.Fatalf("reset: %v", err)
			}
			if got := (observation{r.Entries[0].GetTxBytes(), r.Entries[0].GetRxBytes()}); got != want {
				t.Fatalf("reset counters: %+v", got)
			}
			_, err = stats.GetTraffic(ctx, &statsv1.HysteriaTrafficRequest{Id: "edge", UserId: user})
			if grpcstatus.Code(err) != codes.NotFound {
				t.Fatalf("counter survived reset: %v", err)
			}
			_, err = stats.GetTraffic(ctx, &statsv1.HysteriaTrafficRequest{Id: "edge", UserId: otherUser})
			if grpcstatus.Code(err) != codes.NotFound {
				t.Fatalf("other counter survived reset: %v", err)
			}
		},
		kick: func() {
			r, err := stats.DisconnectUsers(ctx, &statsv1.DisconnectHysteriaUsersRequest{Id: "edge", UserIds: []string{user}})
			if err != nil || !r.GetAcknowledged() {
				t.Fatalf("kick: %v", err)
			}
		},
		logs: func() <-chan []byte {
			stream, err := logsv1.NewHysteriaLogsServiceClient(conn).GetLogs(ctx, &logsv1.HysteriaLogsRequest{Id: "edge"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Header(); err != nil {
				t.Fatal(err)
			}
			events := make(chan []byte, 1)
			go func() {
				defer close(events)
				if row, err := stream.Recv(); err == nil {
					events <- []byte(row.GetEvent())
				}
			}()
			return events
		},
		stop: sync.OnceFunc(func() {
			if _, err := instances.Stop(ctx, &v1.HysteriaInstanceRequest{Id: "edge"}); err != nil {
				t.Fatal(err)
			}
		}),
	}
}

func decodeJSON(t testing.TB, b []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func echoTarget(t testing.TB) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var active sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			active.Go(func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if _, err = io.WriteString(conn, line+line); err != nil {
						return
					}
				}
			})
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done; active.Wait() })
	return listener.Addr().String()
}

func echo(t testing.TB, conn net.Conn, sent string) {
	t.Helper()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	sent += "\n"
	if _, err := io.WriteString(conn, sent); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2*len(sent))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != sent+sent {
		t.Fatalf("proxy payload mismatch: %v", err)
	}
}

type nativeClient struct {
	addr  string
	close func()
}

func (c nativeClient) dial(t testing.TB) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", c.addr, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (f *fixture) startClient(t testing.TB, token, target string) (nativeClient, <-chan error) {
	t.Helper()
	dir := t.TempDir()
	addr := tcpAddress(t)
	source := fmt.Sprintf("server: %s\nauth: %q\ntls:\n  sni: localhost\n  ca: %s\ntcpForwarding:\n  - listen: %s\n    remote: %s\n", f.udp, token, f.cert, addr, target)
	cfg := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(cfg, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(f.binary, "client", "--disable-update-check", "--config", cfg)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.Env = []string{}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	result, done := make(chan error, 1), make(chan struct{})
	go func() { result <- cmd.Wait(); close(done) }()
	stop := sync.OnceFunc(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			_ = cmd.Process.Kill()
			<-done
		}
	})
	t.Cleanup(stop)
	return nativeClient{addr, stop}, result
}

func (f *fixture) client(t testing.TB, token, target string) nativeClient {
	t.Helper()
	c, result := f.startClient(t, token, target)
	until(t, func() bool {
		select {
		case <-result:
			t.Fatal("native client exited before listening")
		default:
		}
		conn, err := net.DialTimeout("tcp", c.addr, 50*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	return c
}

func (f *fixture) rejectedClient(t *testing.T, token, target string) {
	t.Helper()
	c, result := f.startClient(t, token, target)
	defer c.close()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("unexpected client success")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("rejected client did not exit")
	}
}

func (f *fixture) httpLogs(t *testing.T, ctx context.Context) <-chan []byte {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, f.http.URL+instance+"/logs", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("X-API-Key", apiKey)
	resp, err := f.http.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		_ = resp.Body.Close()
		t.Fatalf("logs got HTTP %d", resp.StatusCode)
	}
	out := make(chan []byte, 1)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		var row json.RawMessage
		if json.NewDecoder(resp.Body).Decode(&row) == nil {
			out <- row
		}
	}()
	return out
}
