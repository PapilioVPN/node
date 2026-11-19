package httpapi_test

import (
	"context"

	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
)

type reader struct {
	calls int
	cause error
}

func (r *reader) Temperatures(context.Context) ([]domain.Temperature, error) {
	r.calls++
	zero := int64(0)
	return []domain.Temperature{{ID: "hwmon0/temp1", Device: "cpu", MilliCelsius: -12500, HighMilliCelsius: &zero}}, r.cause
}
func (r *reader) Fans(context.Context) ([]domain.Fan, error) {
	r.calls++
	return []domain.Fan{{ID: "hwmon0/fan1", RPM: 9007199254740993}}, r.cause
}
func (r *reader) PowerSupplies(context.Context) ([]domain.PowerSupply, error) {
	r.calls++
	zero := uint32(0)
	no := false
	return []domain.PowerSupply{{ID: "BAT0", Type: "Battery", CapacityPercent: &zero, Present: &no}}, r.cause
}
