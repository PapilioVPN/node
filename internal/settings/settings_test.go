package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReadsStoragePathAndResolvesTLSFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeSettings(t, path, `management:
  http_address: "[::1]:8080"
  grpc_address: "127.0.0.1:0"
  tls:
    cert_file: certs/server.pem
    key_file: certs/server-key.pem
    client_ca_file: certs/ca.pem
host:
  storage_path: /srv/node/data
`)
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	files := config.Management.TLS
	if files.CertFile != filepath.Join(dir, "certs/server.pem") ||
		files.KeyFile != filepath.Join(dir, "certs/server-key.pem") ||
		files.ClientCAFile != filepath.Join(dir, "certs/ca.pem") {
		t.Fatalf("unexpected paths: %#v", files)
	}
	if config.Host.StoragePath != "/srv/node/data" {
		t.Fatalf("storage path = %q, want /srv/node/data", config.Host.StoragePath)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	const valid = "management:\n  http_address: '127.0.0.1:8080'\n  grpc_address: '127.0.0.1:8081'\n"
	cases := map[string]string{
		"unknown field":         valid + "  surprise: test-secret\n",
		"two documents":         valid + "---\n{}\n",
		"missing port":          strings.Replace(valid, "127.0.0.1:8080", "127.0.0.1", 1),
		"named port":            strings.Replace(valid, ":8080", ":http", 1),
		"partial TLS":           valid + "  tls: {cert_file: cert.pem}\n",
		"cleartext key":         valid + "  api_key: test-secret\n",
		"relative storage path": valid + "host: {storage_path: test-secret}\n",
		"NUL in storage path":   valid + `host: {storage_path: "/test-secret\0"}`,
		"storage path too long": valid + "host: {storage_path: /" + strings.Repeat("x", 4095) + "}\n",
		"large file":            strings.Repeat(" ", maxBytes+1),
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			writeSettings(t, path, contents)
			_, err := Load(path)
			if err == nil {
				t.Fatal("invalid settings were accepted")
			}
			if strings.Contains(err.Error(), "test-secret") {
				t.Fatal("error disclosed a secret")
			}
		})
	}
}

func writeSettings(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadXrayPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	writeSettings(t, path, `management:
  http_address: "127.0.0.1:0"
  grpc_address: "[::1]:0"
xray:
  id: primary
  executable: bin/xray
  config_file: xray.json
  runtime_directory: run
`)
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Xray{
		ID: "primary", Executable: filepath.Join(dir, "bin", "xray"),
		ConfigFile: filepath.Join(dir, "xray.json"), RuntimeDirectory: filepath.Join(dir, "run"),
	}
	if config.Xray != want {
		t.Fatalf("xray config = %+v, want %+v", config.Xray, want)
	}
}

func TestLoadRequiresCredentialsForRemoteXrayManagement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	const config = `management:
  http_address: "0.0.0.0:0"
  grpc_address: "[::1]:0"
xray:
  id: primary
  executable: bin/xray
  config_file: xray.json
`
	writeSettings(t, path, config)
	if _, err := Load(path); err == nil {
		t.Fatal("unprotected remote Xray management was accepted")
	}
	withTLS := strings.Replace(config, "xray:", "  tls: {cert_file: server.pem, key_file: key.pem, client_ca_file: ca.pem}\nxray:", 1)
	writeSettings(t, path, withTLS)
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
