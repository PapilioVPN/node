package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/app"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	instances "github.com/PapilioVPN/node/internal/modules/instances/domain"
)

type binaryReader func(context.Context) (domain.BinaryInfo, error)

func (f binaryReader) ReadBinaryInfo(ctx context.Context) (domain.BinaryInfo, error) { return f(ctx) }

func TestBinaryInfoSelectsExactlyOneConfiguredReader(t *testing.T) {
	ctx := t.Context()
	observed := time.Unix(1700000000, 0).UTC()
	calls := 0
	bindings := map[instances.InstanceID]app.BinaryInfoReader{
		"edge-a": binaryReader(func(received context.Context) (domain.BinaryInfo, error) {
			calls++
			if received != ctx {
				t.Fatal("context changed")
			}
			return domain.BinaryInfo{Version: "26.11.9", ObservedAt: observed}, nil
		}),
		"edge-b": binaryReader(func(context.Context) (domain.BinaryInfo, error) {
			t.Fatal("unselected binary was read")
			return domain.BinaryInfo{}, nil
		}),
		"disabled": nil,
	}
	service := app.NewBinaryInfoService(bindings)
	delete(bindings, "edge-a")
	result, err := service.GetBinaryInfo(ctx, "edge-a")
	if err != nil || result.InstanceID != "edge-a" || result.Binary.Version != "26.11.9" || result.Binary.ObservedAt != observed || calls != 1 {
		t.Fatalf("selected result: %+v, %v; calls=%d", result, err, calls)
	}
	for id, expected := range map[instances.InstanceID]error{"missing": instances.ErrNotFound, " edge-a ": instances.ErrNotFound, "disabled": domain.ErrInspectionDisabled} {
		if _, err := service.GetBinaryInfo(ctx, id); !errors.Is(err, expected) {
			t.Fatalf("%q: %v; want %v", id, err, expected)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.GetBinaryInfo(canceled, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection: %v", err)
	}
}

func TestBinaryInfoFailedOrCanceledReadsReturnNoPartialFacts(t *testing.T) {
	cause := errors.New("native inspection failure")
	for _, cancelDuringRead := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		service := app.NewBinaryInfoService(map[instances.InstanceID]app.BinaryInfoReader{"edge": binaryReader(func(context.Context) (domain.BinaryInfo, error) {
			if cancelDuringRead {
				cancel()
			}
			return domain.BinaryInfo{Version: "partial"}, cause
		})})
		result, err := service.GetBinaryInfo(ctx, "edge")
		cancel()
		if !errors.Is(err, cause) || cancelDuringRead && !errors.Is(err, context.Canceled) || result.InstanceID != "" || result.Binary.Version != "" {
			t.Fatalf("failed read became successful facts: %+v, %v", result, err)
		}
	}
}
