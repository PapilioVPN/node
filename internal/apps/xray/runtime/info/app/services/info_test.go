package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/info/domain"
)

type binaryReader func(context.Context) (domain.BinaryInfo, error)

func (f binaryReader) ReadBinaryInfo(ctx context.Context) (domain.BinaryInfo, error) { return f(ctx) }

func TestBinaryInfoSelectsExactlyOneConfiguredReader(t *testing.T) {
	ctx := t.Context()
	observed := time.Unix(1700000000, 0).UTC()
	calls := 0
	bindings := map[instances.InstanceID]services.BinaryInfoReader{
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
	service := services.NewBinaryInfoService(bindings)
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
		service := services.NewBinaryInfoService(map[instances.InstanceID]services.BinaryInfoReader{"edge": binaryReader(func(context.Context) (domain.BinaryInfo, error) {
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
