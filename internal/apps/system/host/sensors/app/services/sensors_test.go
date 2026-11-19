package services_test

import (
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
	"testing"
)

type reader struct {
	calls []string
	cause error
}

func (r *reader) Temperatures(ctx context.Context) ([]domain.Temperature, error) {
	r.calls = append(r.calls, "temperatures")
	return []domain.Temperature{{ID: "t1", MilliCelsius: -12500}}, r.cause
}
func (r *reader) Fans(ctx context.Context) ([]domain.Fan, error) {
	r.calls = append(r.calls, "fans")
	return []domain.Fan{{ID: "f1", RPM: 0}}, r.cause
}
func (r *reader) PowerSupplies(ctx context.Context) ([]domain.PowerSupply, error) {
	r.calls = append(r.calls, "power")
	return []domain.PowerSupply{}, r.cause
}
func TestIndependentFreshReadsErrorsAndCancellation(t *testing.T) {
	r := &reader{}
	s := services.Service{Reader: r}
	temperatures, err := s.Temperatures(context.Background())
	if err != nil || len(temperatures.Readings) != 1 || temperatures.Readings[0].MilliCelsius != -12500 || temperatures.ObservedAt.IsZero() {
		t.Fatalf("%+v %v", temperatures, err)
	}
	if _, err := s.Fans(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PowerSupplies(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 3 || r.calls[0] != "temperatures" || r.calls[1] != "fans" || r.calls[2] != "power" {
		t.Fatal(r.calls)
	}
	cause := errors.New("native source failure")
	r.cause = cause
	if _, err := s.Temperatures(context.Background()); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := len(r.calls)
	if _, err := s.Fans(ctx); !errors.Is(err, context.Canceled) || before != len(r.calls) {
		t.Fatalf("read after cancellation: %v", err)
	}
}
