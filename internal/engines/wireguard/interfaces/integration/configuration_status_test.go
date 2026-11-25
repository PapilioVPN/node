package integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"

	interfacesv1 "github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/bridge/grpc/proto/v1"
)

type interfaceConfiguration struct {
	PublicKey  []byte   `json:"public_key"`
	ListenPort int      `json:"listen_port"`
	Addresses  []string `json:"addresses"`
}

func readConfiguration(t testing.TB, f fixture, include bool) *interfaceConfiguration {
	t.Helper()
	target := f.address + interfaceURL
	if include {
		target += "?include_configuration=true"
	}
	response, err := f.http.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GetStatus returned %s", response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte("private_key")) {
		t.Fatalf("GetStatus exposed a private key")
	}
	var observed struct {
		Configuration *interfaceConfiguration `json:"configuration"`
	}
	if err := json.Unmarshal(body, &observed); err != nil {
		t.Fatal(err)
	}
	return observed.Configuration
}

func assertInterfaceConfiguration(t *testing.T, f fixture, publicKey []byte, listenPort int, address string) {
	t.Helper()
	if configuration := readConfiguration(t, f, false); configuration != nil {
		t.Fatal("default response included interface configuration")
	}
	configuration := readConfiguration(t, f, true)
	if configuration == nil || !bytes.Equal(configuration.PublicKey, publicKey) ||
		configuration.ListenPort != listenPort || len(configuration.Addresses) != 1 ||
		configuration.Addresses[0] != address {
		t.Fatalf("GetStatus returned unexpected configuration: %+v", configuration)
	}
	client := f.grpcClient(t)
	req := &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName}
	reply, err := client.GetStatus(t.Context(), req)
	if err != nil || reply.GetConfiguration() != nil {
		t.Fatalf("default gRPC GetStatus: %v, %v", reply, err)
	}
	req.IncludeConfiguration = true
	reply, err = client.GetStatus(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	got := reply.GetConfiguration()
	if !bytes.Equal(got.GetPublicKey(), publicKey) || got.GetListenPort() != uint32(listenPort) ||
		len(got.GetAddresses()) != 1 || got.GetAddresses()[0] != address {
		t.Fatalf("gRPC GetStatus returned unexpected configuration: %v", got)
	}
}

func BenchmarkStatusConfiguration(b *testing.B) {
	for _, count := range []int{0, 1000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			f := newFixture(b)
			setPeers(b, count)
			runIP(b, "addr", "add", "192.0.2.10/24", "dev", interfaceName)
			for _, mode := range []struct {
				name    string
				include bool
			}{{"basic", false}, {"detailed", true}} {
				b.Run(mode.name+"/HTTP", func(b *testing.B) {
					for b.Loop() {
						result := readConfiguration(b, f, mode.include)
						if (result != nil) != mode.include || result != nil && len(result.Addresses) != 1 {
							b.Fatalf("unexpected configuration: %+v", result)
						}
					}
				})
				b.Run(mode.name+"/gRPC", func(b *testing.B) {
					client := f.grpcClient(b)
					req := &interfacesv1.WireGuardInterfaceRequest{Name: interfaceName, IncludeConfiguration: mode.include}
					for b.Loop() {
						reply, err := client.GetStatus(b.Context(), req)
						if err != nil {
							b.Fatal(err)
						}
						result := reply.GetConfiguration()
						if (result != nil) != mode.include || result != nil && len(result.GetAddresses()) != 1 {
							b.Fatalf("unexpected configuration: %v", result)
						}
					}
				})
			}
		})
	}
}
