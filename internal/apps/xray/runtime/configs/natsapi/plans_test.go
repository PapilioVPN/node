package natsapi

import (
	"bytes"
	"context"
	"testing"
)

func TestReviewedNATSRequiresExactOperationFields(t *testing.T) {
	h := Handler{}
	ctx := context.Background()
	for _, response := range [][]byte{
		h.Plan(ctx, []byte(`{"sources":[]}`), nil),
		h.ApplyPlan(ctx, []byte(`{"instance_id":"a","application_id":"b"}`), nil),
		h.Application(ctx, []byte(`{"instance_id":"a"}`), nil),
		h.Recover(ctx, []byte(`{"instance_id":"a","application_id":"b","expected_revision":"00","recovery_id":"c"}`), nil),
	} {
		if !bytes.Contains(response, []byte("invalid_argument")) {
			t.Fatal(string(response))
		}
	}
}
