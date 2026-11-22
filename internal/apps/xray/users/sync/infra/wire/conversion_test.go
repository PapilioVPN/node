package wire_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/sync/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/wire"
)

func TestStrictShapePresenceAndExactRevision(t *testing.T) {
	for _, data := range []string{`{"instance_id":"one","instance_id":"two"}`, `{"other":"value"}`, `{"expected":{"config":"18446744073709551616"}}`, `{"sets":[{"inbound_tag":"s","membership_present":true,"unknown":1}]}`, `null`} {
		var r v1.SyncRequest
		err := wire.Decode([]byte(data), &r)
		if err == nil {
			_, err = wire.Request(context.Background(), &r)
		}
		if err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	var r v1.SyncRequest
	if err := wire.Decode([]byte(`{"instance_id":"one","operation_id":"op","mode":"FULL","expected":{"epoch":"epoch","config":"18446744073709551615","control":"9007199254740993","generation":"1"},"selection":["s"],"sets":[{"inbound_tag":"s","membership_present":true}]}`), &r); err != nil {
		t.Fatal(err)
	}
	input, err := wire.Request(context.Background(), &r)
	if err != nil || input.Expected.Config != ^uint64(0) || input.Expected.Control != 9007199254740993 || input.Sets[0].Accounts == nil {
		t.Fatal(input, err)
	}
	r.Sets[0].MembershipPresent = nil
	if _, err = wire.Request(context.Background(), &r); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing membership acknowledgement: %v", err)
	}
}

func FuzzSyncEnvelope(f *testing.F) {
	f.Add([]byte(`{"instance_id":"one","mode":"DELTA"}`))
	f.Add([]byte(`{"sets":[null]}`))
	f.Add([]byte(`{"expected":{"config":"0"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var r v1.SyncRequest
		if wire.Decode(data, &r) == nil {
			_, _ = wire.Request(context.Background(), &r)
		}
	})
}
