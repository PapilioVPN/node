package settings_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/settings"
)

func TestLoadLocalXrayAPI(t *testing.T) {
	for _, endpoint := range []struct{ network, address string }{
		{"unix", "engine/api.sock"}, {"tcp", "127.0.0.1:12345"}, {"tcp", "[::1]:12345"},
	} {
		path := writeConfig(t, apiConfiguration("{network: "+endpoint.network+", address: '"+endpoint.address+"', request_timeout: 2s, max_receive_bytes: 4096, max_concurrent: 4}"))
		cfg, err := settings.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		api := cfg.Instances[0].Xray.API
		address := endpoint.address
		if endpoint.network == "unix" {
			address = filepath.Join(filepath.Dir(path), address)
		}
		if api == nil || api.Network != endpoint.network || api.Address != address || api.RequestTimeout != 2*time.Second || api.MaxReceiveBytes != 4096 || api.MaxConcurrent != 4 {
			t.Fatalf("API settings changed: %+v", api)
		}
		if cfg.Communications.GRPC.Permissions.ReadEngineInfo {
			t.Fatal("API settings enabled an external read grant")
		}
	}
}

func TestXrayAPIRejectsUnboundedOrNonlocalConnections(t *testing.T) {
	valid := "{network: tcp, address: '127.0.0.1:12345', request_timeout: 1s, max_receive_bytes: 4096, max_concurrent: 1}"
	for _, input := range []string{
		"{}",
		strings.Replace(valid, "1s", "0s", 1),
		strings.Replace(valid, "4096", "0", 1),
		strings.Replace(valid, "max_concurrent: 1", "max_concurrent: -1", 1),
		strings.Replace(valid, "tcp", "udp", 1),
		strings.Replace(valid, "127.0.0.1:12345", "192.0.2.1:12345", 1),
		strings.Replace(valid, "127.0.0.1:12345", "localhost:12345", 1),
		strings.Replace(valid, "127.0.0.1:12345", "127.0.0.1:0", 1),
		"{network: unix, address: ' ', request_timeout: 1s, max_receive_bytes: 4096, max_concurrent: 1}",
	} {
		_, err := settings.Load(writeConfig(t, apiConfiguration(input)))
		if !errors.Is(err, settings.ErrInvalidConfig) {
			t.Fatalf("invalid API accepted: %v", err)
		}
	}
}

func apiConfiguration(api string) string {
	return `node: {id: west}
runtime: {startup_timeout: 3s, shutdown_timeout: 3s}
instances:
  - id: edge
    engine: xray
    xray:
      binary: bin/xray
      config_file: config.json
      stop_timeout: 1s
      api: ` + api + "\n"
}
