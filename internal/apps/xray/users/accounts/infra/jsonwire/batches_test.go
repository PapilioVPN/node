package jsonwire_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
)

func TestBatchEnvelopeRetainsExplicitEmptyEmailAndRejectsAmbiguousShape(t *testing.T) {
	removal, err := jsonwire.DecodeRemovals([]byte(`{"instance_id":"a","parallelism":1,"items":[{"inbound_tag":"members","email":""}]}`))
	if err != nil || removal.InstanceID == nil || *removal.InstanceID != "a" || removal.Parallelism != 1 || len(removal.Items) != 1 || removal.Items[0].Email != "" {
		t.Fatal(removal, err)
	}
	addition, err := jsonwire.DecodeAdditions([]byte(`{"items":[{"inbound_tag":"members","configuration":"AQI="}]}`))
	if err != nil || len(addition.Items) != 1 || string(addition.Items[0].Configuration) != "\x01\x02" {
		t.Fatal(addition, err)
	}
	for _, body := range []string{
		`null`, `{}`, `{"items":null}`, `{"items":[]}`, `{"items":[null]}`,
		`{"items":[],"items":[]}`, `{"items":[],"unknown":true}`,
		`{"items":[{"inbound_tag":"a","inbound_tag":"b","email":"x"}]}`,
		`{"items":[{"inbound_tag":"a","email":null}]}`,
		`{"items":[{"inbound_tag":"a","email":"x","unknown":1}]}`,
		`{"items":[{"inbound_tag":"a","email":"x"}],"parallelism":null}`,
		`{"items":[{"inbound_tag":"a","email":"x"}],"parallelism":-1}`,
		`{"items":[{"inbound_tag":"a","email":"x"}],"parallelism":5}`,
		`{"items":[{"inbound_tag":"a","email":"x"}],"parallelism":1.5}`,
		`{"items":[{"inbound_tag":"a","email":"x"}],"instance_id":null}`,
		`{"items":[{"inbound_tag":"a","email":"x"}]} {}`,
		`{"items":[{"inbound_tag":"a","email":"x","configuration":"AQ=="}]}`,
	} {
		if _, err := jsonwire.DecodeRemovals([]byte(body)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(body, err)
		}
	}
	large := `{"items":[` + strings.Repeat(`{"inbound_tag":"a","email":"x"},`, domain.MaxBatchItems) + `{"inbound_tag":"a","email":"x"}]}`
	if _, err := jsonwire.DecodeRemovals([]byte(large)); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
}

func TestSourceBatchEnvelopeAcceptsOneOrderedSourceCollection(t *testing.T) {
	result, err := jsonwire.DecodeAdditions([]byte(`{"sources":[{"name":"base.json","content":"e30="},{"name":"tail.json","content":"e30="}],"items":[{"inbound_tag":"members","email":""},{"inbound_tag":"members","email":"Exact"}],"parallelism":1}`))
	if err != nil || len(result.Sources) != 2 || result.Sources[1].Name != "tail.json" || len(result.Selections) != 2 || result.Selections[0].Email != "" || result.Selections[1].Email != "Exact" || len(result.Items) != 0 {
		t.Fatal("source batch must accept one source collection and ordered selections", err)
	}
	for _, body := range []string{
		`{"sources":[],"items":[{"inbound_tag":"a","email":"x"}]}`,
		`{"sources":null,"items":[{"inbound_tag":"a","email":"x"}]}`,
		`{"sources":[{"name":"a.json","content":"e30="}],"items":[{"inbound_tag":"a"}]}`,
		`{"sources":[{"name":"a.json","content":"e30="}],"items":[{"inbound_tag":"a","email":null}]}`,
		`{"sources":[{"name":"a.json","content":"e30="}],"items":[{"inbound_tag":"a","email":"x","configuration":"AQ=="}]}`,
		`{"sources":[{"name":"a.json","content":"e30="}],"sources":[],"items":[{"inbound_tag":"a","email":"x"}]}`,
		`{"items":[{"inbound_tag":"a","email":"x"}]}`,
	} {
		if _, err := jsonwire.DecodeAdditions([]byte(body)); !errors.Is(err, domain.ErrInvalidArgument) {
			t.Fatal(body, err)
		}
	}
	if _, err := jsonwire.DecodeRemovals([]byte(`{"sources":[{"name":"a.json","content":"e30="}],"items":[{"inbound_tag":"a","email":"x"}]}`)); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal(err)
	}
}

func TestBatchEncodingPreservesEvidenceWithoutPrivateCauses(t *testing.T) {
	input := domain.Batch{Operation: domain.Operation{Generation: ^uint64(0), Effect: domain.Partial}, Items: []domain.ItemResult{{Index: 0, Attempted: true, Operation: domain.Operation{Effect: domain.Completed, Acknowledged: true}}, {Index: 1, Err: errors.New("private native cause"), Operation: domain.Operation{Effect: domain.NotApplied}}}}
	data, err := jsonwire.EncodeBatch(input, []string{"", "not_attempted"}, "invalid_argument")
	if err != nil || !json.Valid(data) || strings.Contains(string(data), "private native cause") || !strings.Contains(string(data), `"generation":"18446744073709551615"`) || !strings.Contains(string(data), `"error":"not_attempted"`) {
		t.Fatal(string(data), err)
	}
}

func FuzzAccountBatchEnvelope(f *testing.F) {
	f.Add([]byte(`{"sources":[{"name":"a.json","content":"e30="}],"items":[{"inbound_tag":"a","email":""}]}`))
	f.Add([]byte(`{"items":[{"inbound_tag":"a","configuration":"AQ=="}]}`))
	f.Add([]byte(`{"items":[{"inbound_tag":"a","email":""}],"parallelism":1}`))
	f.Add([]byte(`{"items":null}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, decode := range []func([]byte) error{
			func(data []byte) error { _, err := jsonwire.DecodeAdditions(data); return err },
			func(data []byte) error { _, err := jsonwire.DecodeRemovals(data); return err },
		} {
			if err := decode(data); err == nil && !json.Valid(data) {
				t.Fatal("accepted invalid JSON")
			}
		}
	})
}
