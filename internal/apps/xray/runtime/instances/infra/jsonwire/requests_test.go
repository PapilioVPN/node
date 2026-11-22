package jsonwire

import (
	"testing"
)

func TestStrictRecoveryInputs(t *testing.T) {
	for _, data := range []string{`null`, `{"preserve":true,"execution_id":"a","execution_id":"b"}`, `{"preserve":null,"execution_id":"a"}`, `{"preserve":true}`, `{"preserve":true,"execution_id":"a","unknown":1}`, `{"preserve":true,"execution_id":"a"}{}`, `{"preserve":"false","execution_id":"a"}`} {
		if _, err := Decode([]byte(data), "execution_id preserve", ""); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	if _, err := Decode([]byte(`{"preserve":false,"execution_id":"a"}`), "execution_id preserve", ""); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "00", "+1", "-1", "18446744073709551616"} {
		if _, err := Revision(value); err == nil {
			t.Fatal("revision", value)
		}
	}
}
func FuzzRecoveryEnvelope(f *testing.F) {
	f.Add([]byte(`{"instance_id":"one","expected_revision":"0","execution_id":"a","owner":"controller","ttl_seconds":1,"policy":"stop"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data, "instance_id expected_revision execution_id owner ttl_seconds policy", "")
	})
}
