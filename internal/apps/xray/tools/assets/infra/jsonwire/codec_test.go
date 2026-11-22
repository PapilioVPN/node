package jsonwire

import (
	"strings"
	"testing"
)

func TestActivationPresencePrecisionAndDuplicateRejection(t *testing.T) {
	prefix := `{"instance_id":"one","expected_revision":"18446744073709551615","execution_id":"` + strings.Repeat("a", 32) + `"`
	id, q, err := Activation([]byte(prefix+`,"release_id":"`+strings.Repeat("b", 64)+`"}`), "", true)
	if err != nil || id != "one" || q.ExpectedRevision != ^uint64(0) {
		t.Fatal(id, q, err)
	}
	for _, data := range []string{`{}`, prefix + `}`, prefix + `,"expected_revision":"1"}`, `{"expected_revision":0}`, `{"expected_revision":"00"}`, `{"expected_revision":null}`, `{"execution_id":null}`, `{"instance_id":"a","instance_id":"b"}`, `{} {}`, `null`} {
		if _, _, err := Activation([]byte(data), "", true); err == nil {
			t.Fatal("accepted invalid activation", data)
		}
	}
	if _, _, err := Activation([]byte(prefix+`,"release_id":"x"}`), "one", false); err == nil {
		t.Fatal("mixed external instance or rollback release accepted")
	}
}
func TestNestedStageRejectsUnknownDuplicateNullAndBadBase64(t *testing.T) {
	file := `{"sha256":"abc","content":"AQ=="}`
	valid := `{"candidate":{"binary":` + file + `,"geoip":` + file + `,"geosite":` + file + `}}`
	_, c, err := Stage([]byte(valid), "one")
	if err != nil || len(c.Binary.Content) != 1 || c.Binary.Content[0] != 1 {
		t.Fatal(c, err)
	}
	for _, data := range []string{`{}`, `{"candidate":null}`, strings.Replace(valid, `"sha256":"abc"`, `"sha256":"abc","sha256":"other"`, 1), strings.Replace(valid, `"binary":`+file, `"binary":null`, 1), strings.Replace(valid, `AQ==`, `?`, 1), strings.Replace(valid, `"binary":`, `"unknown":`, 1), strings.Replace(valid, `"content":"AQ=="`, `"content":null`, 1)} {
		if _, _, err := Stage([]byte(data), "one"); err == nil {
			t.Fatal("accepted invalid stage", data)
		}
	}
}
func FuzzAssetJSON(f *testing.F) {
	f.Add([]byte(`{"instance_id":"one"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = Inspect(data, "")
		_, _, _ = Stage(data, "")
		_, _, _ = Activation(data, "", true)
	})
}
