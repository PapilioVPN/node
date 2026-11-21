//go:build integration && linux

package process

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/proto"
)

func TestNativeMultiSourceCompilationPreservesMergeNamesAndNativeAssetContext(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	work := filepath.Join(root, "details-with-tail-in-workspace")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	asset := []byte("unique-relative-certificate-source")
	if err := os.WriteFile(filepath.Join(root, "cert.pem"), asset, 0600); err != nil {
		t.Fatal(err)
	}
	admission, _ := NewAdmission(2)
	b, _ := New(Config{Binary: binary, Directory: root, WorkDirectory: work}, admission)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	parentEnv := os.Getenv("xray.location.cert")
	baseContent, _ := json.Marshal(map[string]any{"env": map[string]string{"xray.location.cert": root}, "outbounds": []any{map[string]any{"tag": "base", "protocol": "freedom", "streamSettings": map[string]any{"security": "tls", "tlsSettings": map[string]any{"certificates": []any{map[string]any{"certificateFile": "cert.pem", "usage": "verify"}}}}}}})
	base := domain.Source{Name: "base.json", Content: baseContent}
	for _, test := range []struct {
		name        string
		first, last string
	}{{"next.yaml", "next", "base"}, {"tail-next.yaml", "base", "next"}} {
		result, err := b.Compile(ctx, []domain.Source{base, {Name: test.name, Content: []byte("outbounds:\n  - tag: next\n    protocol: blackhole\n")}})
		if err != nil {
			t.Fatal(err)
		}
		var config core.Config
		if err := proto.Unmarshal(result.Content, &config); err != nil {
			t.Fatal(err)
		}
		if len(config.Outbound) != 2 || config.Outbound[0].Tag != test.first || config.Outbound[1].Tag != test.last || !bytes.Contains(result.Content, asset) {
			t.Fatalf("native names/order/relative asset lost: %v", &config)
		}
		digest := sha256.Sum256(result.Content)
		if result.SHA256 != hex.EncodeToString(digest[:]) {
			t.Fatal("wrong compiled content digest")
		}
	}
	if os.Getenv("xray.location.cert") != parentEnv {
		t.Fatal("native build mutated Node environment")
	}
	// Empty protobuf is the native default message, not an absent source.
	empty, err := b.Compile(ctx, []domain.Source{{Name: "empty.pb"}})
	if err != nil || len(empty.Content) != 0 {
		t.Fatalf("empty protobuf rejected: %v", err)
	}
	// Existing protobuf fields and unrecognized extension bytes remain native.
	original := &core.Config{}
	unknown := []byte{0xa0, 0x06, 0x01}
	original.ProtoReflect().SetUnknown(unknown)
	content, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	result, err := b.Compile(ctx, []domain.Source{{Name: "native.protobuf", Content: content}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.Config
	if proto.Unmarshal(result.Content, &decoded) != nil || !bytes.Equal(decoded.ProtoReflect().GetUnknown(), unknown) {
		t.Fatal("unknown native configuration fields lost")
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 {
		t.Fatalf("compiled staging leaked: %v %v", entries, err)
	}
}

func TestNativeCompilationOutputBoundRejectionAndPrivateCleanup(t *testing.T) {
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	a, _ := NewAdmission(1)
	b, _ := New(Config{Binary: binary, Directory: work, WorkDirectory: work, MaxOutputBytes: 1}, a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := b.Compile(ctx, []domain.Source{{Name: "base.json", Content: []byte(`{"outbounds":[{"protocol":"freedom"}]}`)}})
	if !errors.Is(err, domain.ErrTooLarge) || result.Content != nil {
		t.Fatalf("output limit: %+v %v", result, err)
	}
	b.config.MaxOutputBytes = domain.MaxCompiledBytes
	result, err = b.Compile(ctx, []domain.Source{{Name: "base.json", Content: []byte(`{"outbounds":[{"protocol":"not-a-native-protocol"}]}`)}})
	if !errors.Is(err, domain.ErrRejected) || result.Content != nil {
		t.Fatalf("native rejection: %+v %v", result, err)
	}
	result, err = b.Compile(ctx, []domain.Source{{Name: "../outside.json", Content: []byte(`{}`)}})
	if !errors.Is(err, domain.ErrInvalidArgument) || result.Content != nil {
		t.Fatal("native path encoding accepted traversal")
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 || len(a.slots) != 0 {
		t.Fatalf("unfinished conversion resources: %v %v", entries, err)
	}
}
