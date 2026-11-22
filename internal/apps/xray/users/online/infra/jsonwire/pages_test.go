package jsonwire

import (
	"strings"
	"testing"
)

func TestPageEnvelopeIsStrict(t *testing.T) {
	for _, s := range []string{`{}`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":null}`, `{"instance_id":"a","page_size":null}`, `{"instance_id":"a","page_size":1.2}`, `{"instance_id":"a","page_size":"2"}`, `{"instance_id":"a","unknown":1}`, `{"instance_id":"a","page_token":null}`, `{"instance_id":"a"} {}`} {
		if _, err := DecodePage([]byte(s), false); err == nil {
			t.Fatal(s)
		}
	}
	if _, err := DecodePage([]byte(`{"instance_id":"a","email":"x"}`), false); err == nil {
		t.Fatal("collection accepted email")
	}
	if _, err := DecodePage([]byte(`{"instance_id":"a"}`), true); err == nil {
		t.Fatal("missing email")
	}
	if r, err := DecodePage([]byte(`{"instance_id":"a","page_size":50,"page_token":"opaque"}`), false); err != nil || r.PageSize != 50 || r.PageToken != "opaque" {
		t.Fatal(r, err)
	}
	if _, err := DecodePage([]byte(strings.Repeat(" ", 32769)), false); err == nil {
		t.Fatal("input bound")
	}
}
func FuzzPageEnvelope(f *testing.F) {
	f.Add([]byte(`{"instance_id":"a","page_size":50}`))
	f.Add([]byte(`{"instance_id":"a","email":" A>>>b "}`))
	f.Fuzz(func(t *testing.T, data []byte) { DecodePage(data, true); DecodePage(data, false) })
}
