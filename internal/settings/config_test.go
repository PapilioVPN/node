package settings_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/settings"
)

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	t.Setenv("NODE_ID", "unexpected-environment-node")
	config, err := settings.Load(writeConfig(t, `node:
  id: node-west
communications:
  grpc:
    enabled: true
    listen: "127.0.0.1:9443"
`))
	if err != nil {
		t.Fatal(err)
	}
	if config.Node.ID != "node-west" || !config.Communications.GRPC.Enabled || config.Communications.GRPC.Listen != "127.0.0.1:9443" {
		t.Fatalf("unexpected configuration: %+v", config)
	}
}

func TestLoadRejectsInvalidConfiguration(t *testing.T) {
	cases := map[string]string{
		"missing identity":          "communications: {}\n",
		"blank identity":            "node: {id: '  '}\n",
		"unknown field":             "node: {id: west, typo: true}\n",
		"duplicate key":             "node: {id: west, id: east}\n",
		"multiple documents":        "node: {id: west}\n---\nnode: {id: east}\n",
		"trailing invalid document": "node: {id: west}\n---\n[\n",
		"missing listener":          "node: {id: west}\ncommunications: {grpc: {enabled: true}}\n",
		"invalid listener":          "node: {id: west}\ncommunications: {grpc: {enabled: true, listen: localhost}}\n",
		"named port":                "node: {id: west}\ncommunications: {grpc: {enabled: true, listen: 'localhost:https'}}\n",
		"out of range port":         "node: {id: west}\ncommunications: {grpc: {enabled: true, listen: 'localhost:70000'}}\n",
		"unsupported transport":     "node: {id: west}\ncommunications: {nats: {enabled: true}}\n",
		"invalid type":              "node: {id: west}\ncommunications: {grpc: {enabled: 'not-a-boolean'}}\n",
		"empty file":                "",
	}
	for name, contents := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := settings.Load(writeConfig(t, contents)); !errors.Is(err, settings.ErrInvalidConfig) {
				t.Fatalf("expected invalid configuration, got %v", err)
			}
		})
	}
}

func TestLoadDoesNotExposeConfigurationValues(t *testing.T) {
	const sensitive = "private-value-that-must-not-appear-in-errors"
	_, err := settings.Load(writeConfig(t, "node: {id: west}\ncommunications: {grpc: {enabled: '"+sensitive+"'}}\n"))
	if !errors.Is(err, settings.ErrInvalidConfig) || strings.Contains(err.Error(), sensitive) {
		t.Fatalf("configuration error was not sanitized: %v", err)
	}
}

func TestLoadOptionalGRPC(t *testing.T) {
	if _, err := settings.Load(writeConfig(t, "node: {id: west}\n")); err != nil {
		t.Fatalf("an inactive transport must not require connection settings: %v", err)
	}
	if _, err := settings.Load(writeConfig(t, "node: {id: west}\ncommunications: {grpc: {enabled: true, listen: '[::1]:0'}}\n")); err != nil {
		t.Fatalf("expected an IPv6 listener with an OS-assigned port: %v", err)
	}
}

func TestLoadPreservesFilesystemError(t *testing.T) {
	_, err := settings.Load(filepath.Join(t.TempDir(), "missing.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lost filesystem cause: %v", err)
	}
}
