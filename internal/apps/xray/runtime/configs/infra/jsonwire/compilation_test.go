package jsonwire

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func TestCompilationEnvelopePreservesOrderAndArbitraryNativeBytes(t *testing.T) {
	for _, count := range []int{1, 8, 32} {
		var sources []map[string]any
		for i := range count {
			sources = append(sources, map[string]any{"name": fmt.Sprintf("%02d.json", i), "content": []byte{0, 0xff, 1}})
		}
		data, _ := json.Marshal(map[string]any{"instance_id": "a", "sources": sources})
		id, decoded, err := DecodeCompilation(data)
		if err != nil || id != "a" || len(decoded) != count {
			t.Fatalf("decode %d: %v", count, err)
		}
		for i, source := range decoded {
			if source.Name != fmt.Sprintf("%02d.json", i) || len(source.Content) != 3 || source.Content[1] != 0xff {
				t.Fatal("source order/bytes changed")
			}
		}
	}
	encoded, err := EncodeCompilation(domain.Compilation{InstanceID: "a", Content: []byte{0xff}, SHA256: "digest", PreparedAt: time.Unix(0, 0).UTC(), Duration: 9007199254740993})
	if err != nil || !strings.Contains(string(encoded), `"duration_ns":"9007199254740993"`) || !strings.Contains(string(encoded), `"content":"/w=="`) {
		t.Fatal("compiled bytes or integer duration lost precision")
	}
}

func TestCompilationEnvelopeRejectsAmbiguousOrUnboundedInput(t *testing.T) {
	const valid = `{"instance_id":"a","sources":[{"name":"a.json","content":"e30="}]}`
	for _, data := range []string{`null`, `[]`, `{}`, valid + ` {}`, strings.Replace(valid, `"instance_id":"a"`, `"instance_id":"a","instance_id":"b"`, 1), strings.Replace(valid, `"name":"a.json"`, `"name":"a.json","name":"b.json"`, 1), strings.Replace(valid, `"content":"e30="`, `"content":"e30=","content":"e30="`, 1), strings.Replace(valid, `"content"`, `"unknown"`, 1), strings.Replace(valid, `"e30="`, `"invalid-base64"`, 1), `{"instance_id":"a","sources":null}`} {
		if _, _, err := DecodeCompilation([]byte(data)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("ambiguous JSON accepted: %s", data)
		}
	}
	if _, _, err := DecodeCompilation(make([]byte, (2<<20)+1)); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatal("encoded envelope byte limit ignored")
	}
	sources := strings.Repeat(`{"name":"a.json","content":"e30="},`, 32) + `{"name":"last.json","content":"e30="}`
	if _, _, err := DecodeCompilation([]byte(`{"instance_id":"a","sources":[` + sources + `]}`)); !errors.Is(err, domain.ErrTooLarge) {
		t.Fatal("source count not bounded during decode")
	}
}

func FuzzCompilationEnvelope(f *testing.F) {
	for _, seed := range []string{`{"instance_id":"a","sources":[{"name":"base.json","content":"e30="}]}`, `{"instance_id":"a","sources":[]}`, `{"sources":null}`, `{"instance_id":"a","instance_id":"b"}`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, sources, err := DecodeCompilation(data)
		if err == nil && len(sources) > domain.MaxSources {
			t.Fatal("decoder exceeded source count bound")
		}
	})
}
