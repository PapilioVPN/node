package settings

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHysteria(t *testing.T) {
	const local = "management:\n  http_address: '127.0.0.1:0'\n  grpc_address: '127.0.0.1:0'\n"
	const valid = `hysteria:
  instances:
    - id: edge
      executable: bin/hysteria
      config_file: edge.yml
      runtime_directory: run/edge
`
	for _, test := range []struct {
		name, source, wantError string
	}{
		{"absent", local, ""},
		{"empty", local + "hysteria: {instances: []}\n", ""},
		{"configured", local + valid, ""},
		{"multiple", local + valid + "    - {id: another, executable: bin/hysteria, config_file: another.yml, runtime_directory: run/another}\n", ""},
		{"duplicate", local + valid + "    - {id: edge, executable: bin/hysteria, config_file: other.yml, runtime_directory: run/other}\n", "duplicate"},
		{"bad id", local + strings.Replace(valid, "id: edge", "id: TEST-SECRET", 1), "id must"},
		{"blank path", local + strings.Replace(valid, "run/edge", `'  '`, 1), "runtime_directory"},
		{"nul path", local + strings.Replace(valid, "bin/hysteria", `"TEST-SECRET\0"`, 1), "NUL"},
		{"unauthenticated remote", strings.Replace(local, "127.0.0.1:0", "0.0.0.0:0", 1) + valid, "remote hysteria management requires TLS"},
		{"authenticated remote", strings.Replace(local, "127.0.0.1:0", "0.0.0.0:0", 1) + "  api_key: fixture-key\n  tls: {cert_file: cert.pem, key_file: key.pem}\n" + valid, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			writeSettings(t, path, test.source)
			_, err := Load(path)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) || strings.Contains(err.Error(), "TEST-SECRET") {
				t.Fatalf("expected sanitized %q error, got %v", test.wantError, err)
			}
		})
	}
}

func TestLoadHysteriaResolvesInstancePaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeSettings(t, path, `management:
  http_address: '127.0.0.1:0'
  grpc_address: '127.0.0.1:0'
hysteria:
  instances:
    - {id: edge, executable: bin/hysteria, config_file: /etc/node/edge.yml, runtime_directory: run/edge}
`)
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := HysteriaInstance{
		ID: "edge", Executable: filepath.Join(dir, "bin/hysteria"),
		ConfigFile: "/etc/node/edge.yml", RuntimeDirectory: filepath.Join(dir, "run/edge"),
	}
	if got := config.Hysteria.Instances; len(got) != 1 || got[0] != want {
		t.Fatalf("instance paths: got %+v, want %+v", got, want)
	}
}
