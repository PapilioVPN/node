package jsonwire_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/infra/jsonwire"
)

func TestStrictSelectorsAndPrivateErrors(t *testing.T) {
	for _, data := range []string{`null`, `[]`, `{}`, `{"instance_id":null}`, `{"instance_id":"edge","cursor":null}`, `{"instance_id":"edge","instance_id":"other","cursor":"a"}`, `{"instance_id":"edge","cursor":"a","wait_ms":-1}`, `{"instance_id":"edge","cursor":"a","unknown":true}`, `{"instance_id":"edge","cursor":"a"} {}`} {
		if _, err := jsonwire.Decode([]byte(data), true); err == nil {
			t.Fatal(data)
		}
	}
	if _, err := jsonwire.Decode([]byte(`{"instance_id":"edge","cursor":"a"}`), false); err == nil {
		t.Fatal("tail accepted cursor")
	}
	r, err := jsonwire.Decode([]byte(`{"instance_id":"edge","cursor":"a","limit":50,"wait_ms":1000}`), true)
	if err != nil || r.Limit != 50 || r.WaitMilliseconds != 1000 {
		t.Fatal(r, err)
	}
	if string(jsonwire.Failure(errors.New("password=private"))) != `{"error":"unavailable"}` {
		t.Fatal("private cause published")
	}
	data, err := jsonwire.Encode(domain.Page{Generation: 1 << 63, Records: []domain.Record{}, Gaps: []domain.Gap{}})
	if err != nil || !strings.Contains(string(data), `"generation":"9223372036854775808"`) || !strings.Contains(string(data), `"records":[]`) {
		t.Fatal(string(data), err)
	}
}
func FuzzLogRequests(f *testing.F) {
	for _, v := range []string{`{}`, `{"instance_id":"edge","cursor":"a","wait_ms":1000}`, `null`} {
		f.Add([]byte(v), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, follow bool) { jsonwire.Decode(data, follow) })
}
