package services_test

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/node/runtime/domain"
	"testing"
)

type probes struct {
	result domain.Diagnostics
	cause  error
	calls  int
}

func (reader *probes) Read(context.Context) (domain.Diagnostics, error) {
	reader.calls++
	return reader.result, reader.cause
}
func TestDiagnosticsDistinguishAbsentReadyAndUnreachableResources(t *testing.T) {
	reader := &probes{}
	service := services.Service{Probes: reader}
	ctx := context.Background()
	result, err := service.Diagnostics(ctx)
	if err != nil || result.Healthy || result.Diagnostics.HTTP != nil {
		t.Fatalf("%+v %v", result, err)
	}
	reader.result.HTTP = &domain.Listener{Ready: true}
	result, err = service.Diagnostics(ctx)
	if err != nil || !result.Healthy || result.Diagnostics.NATS != nil {
		t.Fatalf("%+v %v", result, err)
	}
	reader.result.NATS = &domain.Broker{State: "RECONNECTING", ErrorCode: "not_connected"}
	result, err = service.Diagnostics(ctx)
	if err != nil || result.Healthy {
		t.Fatalf("%+v %v", result, err)
	}
	reader.result.NATS = &domain.Broker{State: "CONNECTED", ProbeSucceeded: true}
	result, err = service.Diagnostics(ctx)
	if err != nil || !result.Healthy {
		t.Fatalf("%+v %v", result, err)
	}
	cause := errors.New("private native cause")
	reader.cause = cause
	if _, err := service.Diagnostics(ctx); !errors.Is(err, cause) {
		t.Fatal(err)
	}
}
