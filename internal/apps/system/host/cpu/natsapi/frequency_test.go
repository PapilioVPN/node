package natsapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/cpu/natsapi"
	"github.com/nats-io/nats.go"
)

func TestNATSFrequencyPreservesPreciseFactsAndIssues(t *testing.T) {
	handler := natsapi.Handler{Timeout: time.Second, Service: services.Service{FrequencyReader: frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) { return sampleFrequencies(), nil })}}
	body := handler.ReadFrequency(context.Background(), []byte("{}"), nil)
	var got struct {
		Present  bool `json:"interface_present"`
		Policies []struct {
			Current     string                               `json:"scaling_current_khz"`
			Min         *string                              `json:"hardware_min_khz"`
			Hardware    *string                              `json:"hardware_current_khz"`
			Available   []string                             `json:"available_khz"`
			Unavailable []struct{ Attribute, Reason string } `json:"unavailable"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Present || len(got.Policies) != 1 {
		t.Fatalf("lost policies: %s", body)
	}
	row := got.Policies[0]
	if row.Current != "9007199254740993" || row.Min == nil || *row.Min != "0" || row.Hardware != nil || len(row.Available) != 1 || row.Available[0] != "9007199254740993" || len(row.Unavailable) != 1 || row.Unavailable[0].Reason != "permission_denied" {
		t.Fatalf("lost precise facts: %s", body)
	}
}

func TestNATSFrequencyRejectsShapeAndBoundsDeadline(t *testing.T) {
	handler := natsapi.Handler{Timeout: 10 * time.Millisecond}
	for _, input := range []string{"", "null", "[]", "{}{}", `{"unknown":1}`, strings.Repeat(" ", 1025)} {
		got := handler.ReadFrequency(context.Background(), []byte(input), nil)
		if string(got) != `{"error":"invalid_argument"}` && string(got) != `{"error":"resource_exhausted"}` {
			t.Fatalf("input accepted: %s", got)
		}
	}
	if got := handler.ReadFrequency(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {"bad"}}); string(got) != `{"error":"invalid_argument"}` {
		t.Fatal(string(got))
	}
	handler.Service.FrequencyReader = frequencyReaderFunc(func(ctx context.Context) (domain.Frequencies, error) {
		<-ctx.Done()
		return domain.Frequencies{}, ctx.Err()
	})
	for _, deadline := range []time.Time{time.Now().Add(-time.Second), time.Now().Add(time.Hour)} {
		got := handler.ReadFrequency(context.Background(), []byte("{}"), nats.Header{"Papilio-Deadline": {deadline.UTC().Format(time.RFC3339Nano)}})
		if string(got) != `{"error":"deadline_exceeded"}` {
			t.Fatal(string(got))
		}
	}
	handler.Service.FrequencyReader = frequencyReaderFunc(func(context.Context) (domain.Frequencies, error) {
		return sampleFrequencies(), errors.New("private sysfs path")
	})
	if got := handler.ReadFrequency(context.Background(), []byte("{}"), nil); string(got) != `{"error":"unavailable"}` {
		t.Fatal(string(got))
	}
}
