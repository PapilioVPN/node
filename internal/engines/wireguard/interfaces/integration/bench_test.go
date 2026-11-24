package integration_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/lifecycle"
	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/native"
)

func BenchmarkCheckpointRecovery(b *testing.B) {
	requireIsolatedNetwork(b)
	for _, peers := range []int{0, 1000} {
		b.Run(fmt.Sprintf("peers-%d", peers), func(b *testing.B) {
			directory := filepath.Join(b.TempDir(), "state")
			if err := os.Mkdir(directory, 0700); err != nil {
				b.Fatal(err)
			}
			store := &native.CheckpointStore{Path: directory}
			control := new(native.Control)
			if err := store.Open(b.Context()); err != nil {
				b.Fatal(err)
			}
			if err := control.Open(b.Context()); err != nil {
				b.Fatal(err)
			}
			service := lifecycle.New(interfaceName, control, store)
			source := []byte(`{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`)
			link, err := service.Start(b.Context(), interfaceName, source)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { runIP(b, "link", "del", "dev", interfaceName) })
			if err := control.Close(); err != nil {
				b.Fatal(err)
			}
			if err := store.Close(); err != nil {
				b.Fatal(err)
			}
			setPeers(b, peers)
			b.ReportAllocs()
			for b.Loop() {
				reopened := &native.CheckpointStore{Path: directory}
				control := new(native.Control)
				if err := reopened.Open(b.Context()); err != nil {
					b.Fatal(err)
				}
				if err := control.Open(b.Context()); err != nil {
					b.Fatal(err)
				}
				recovered := lifecycle.New(interfaceName, control, reopened)
				if err := recovered.Recover(b.Context()); err != nil {
					b.Fatal(err)
				}
				if recovered.AppliedHash(link) != link.AppliedSHA256 {
					b.Fatal("readiness did not recover the confirmed fingerprint")
				}
				if err := control.Close(); err != nil {
					b.Fatal(err)
				}
				if err := reopened.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkConfigValidation(b *testing.B) {
	f := newFixture(b)
	for _, count := range []int{1, 16} {
		key := sha256.Sum256([]byte("WireGuard validation benchmark"))
		addresses := make([]string, count)
		for i := range addresses {
			addresses[i] = fmt.Sprintf("192.0.2.%d/24", i+1)
		}
		source, err := json.Marshal(struct {
			PrivateKey []byte   `json:"private_key"`
			Addresses  []string `json:"addresses"`
		}{PrivateKey: key[:], Addresses: addresses})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("addresses-%d", count), func(b *testing.B) {
			b.Run("HTTP", func(b *testing.B) {
				benchValidation(b, source, func() bool {
					return f.validateHTTP(b, interfaceName, source, http.StatusOK).Valid
				})
			})
			b.Run("gRPC", func(b *testing.B) {
				client := f.grpcClient(b)
				req := &interfacesv1.WireGuardValidateConfigRequest{Name: interfaceName, ConfigJson: source}
				benchValidation(b, source, func() bool {
					ctx, cancel := context.WithTimeout(b.Context(), time.Second)
					defer cancel()
					reply, err := client.ValidateConfig(ctx, req)
					if err != nil {
						b.Fatal(err)
					}
					return reply.GetValid()
				})
			})
		})
	}
}

func benchValidation(b *testing.B, source []byte, validate func() bool) {
	b.Helper()
	if !validate() {
		b.Fatal("warm-up rejected the fixture")
	}
	b.SetBytes(int64(len(source)))
	b.ReportAllocs()
	for b.Loop() {
		if !validate() {
			b.Fatal("validation rejected the fixture")
		}
	}
}

func BenchmarkInterfaceStatus(b *testing.B) {
	for _, peers := range []int{0, 1000} {
		b.Run(fmt.Sprintf("peers-%d", peers), func(b *testing.B) {
			f := newFixture(b)
			setPeers(b, peers)
			runIP(b, "link", "set", "dev", interfaceName, "up")
			want := observation{Name: interfaceName, MTU: 1420, Up: true}
			b.Run("HTTP", func(b *testing.B) {
				benchmarkStatus(b, want, func() observation { return f.readHTTP(b) })
			})
			b.Run("gRPC", func(b *testing.B) {
				client := f.grpcClient(b)
				benchmarkStatus(b, want, func() observation { return readGRPC(b, client) })
			})
			b.Run("ip", func(b *testing.B) {
				benchmarkStatus(b, want, func() observation { return readIP(b) })
			})
		})
	}
}

func benchmarkStatus(b *testing.B, want observation, read func() observation) {
	b.Helper()
	if got := read(); got != want {
		b.Fatalf("warm-up status = %+v, want %+v", got, want)
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := read(); got != want {
			b.Fatalf("status = %+v, want %+v", got, want)
		}
	}
}

func setPeers(b *testing.B, peers int) {
	b.Helper()
	var config strings.Builder
	config.WriteString("[Interface]\n")
	for i := range peers {
		key := sha256.Sum256([]byte(strconv.Itoa(i)))
		fmt.Fprintf(&config, "[Peer]\nPublicKey = %s\n", base64.StdEncoding.EncodeToString(key[:]))
	}
	cmd := exec.CommandContext(b.Context(), "wg", "addconf", interfaceName, "/dev/stdin")
	cmd.Stdin = strings.NewReader(config.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		b.Fatalf("wg addconf: %v: %s", err, output)
	}
	output, err := exec.CommandContext(b.Context(), "wg", "show", interfaceName, "peers").Output()
	if err != nil || len(strings.Fields(string(output))) != peers {
		b.Fatalf("expected %d peers, wg show returned %v", peers, err)
	}
}

func readIP(b *testing.B) observation {
	b.Helper()
	output, err := exec.CommandContext(b.Context(), "ip", "-j", "-d", "link", "show", "dev", interfaceName).Output()
	if err != nil {
		b.Fatal(err)
	}
	var links []struct {
		Name  string   `json:"ifname"`
		MTU   uint32   `json:"mtu"`
		Flags []string `json:"flags"`
		Info  struct {
			Kind string `json:"info_kind"`
		} `json:"linkinfo"`
	}
	if err := json.Unmarshal(output, &links); err != nil {
		b.Fatal(err)
	}
	if len(links) != 1 || links[0].Info.Kind != "wireguard" {
		b.Fatalf("expected one WireGuard interface, got %s", output)
	}
	link := links[0]
	return observation{Name: link.Name, MTU: link.MTU, Up: slices.Contains(link.Flags, "UP")}
}
