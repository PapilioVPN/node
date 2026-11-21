package jsonwire

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/loggers/domain"
)

func TestLoggerEnvelopeSelectionAndExactOperationEvidence(t *testing.T) {
	for _, input := range []string{`{}`, `[]`, `{"instance_id":"a","instance_id":"b"}`, `{"instance_id":"a","unknown":true}`, `{"instance_id":"a"} {}`, strings.Repeat(" ", 2049)} {
		if _, err := DecodeRequest([]byte(input)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatalf("ambiguous logger input accepted: %s", input)
		}
	}
	data, err := EncodeOperation(domain.Operation{InstanceID: "a", Generation: math.MaxUint64, Effect: domain.Unknown, Acknowledged: true, RequiresInstanceRestart: true, Duration: 9007199254740993}, "unavailable")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Operation struct {
			Generation string `json:"generation"`
			Effect     string `json:"effect"`
			Ack        bool   `json:"acknowledged"`
			Requires   bool   `json:"requires_instance_restart"`
			Duration   string `json:"duration_ns"`
		} `json:"operation"`
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil || result.Operation.Generation != "18446744073709551615" || result.Operation.Duration != "9007199254740993" || result.Operation.Effect != "unknown" || !result.Operation.Ack || !result.Operation.Requires || result.Error != "unavailable" {
		t.Fatalf("lossy control evidence %s", data)
	}
}
