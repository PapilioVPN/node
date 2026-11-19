package sys

import (
	"cmp"
	"context"
	"errors"
	"github.com/PapilioVPN/node/internal/apps/system/host/sensors/domain"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func (Reader) PowerSupplies(ctx context.Context) ([]domain.PowerSupply, error) {
	return powerSupplies(ctx, "/sys")
}

// The native uevent projection reads one attribute set per supply instead of
// querying every individual property or probing unrelated writable attributes.
func powerSupplies(ctx context.Context, root string) ([]domain.PowerSupply, error) {
	sources, err := entries(ctx, filepath.Join(root, "class/power_supply"))
	if err != nil {
		return nil, err
	}
	rows := make([]domain.PowerSupply, 0, len(sources))
	for _, source := range sources {
		value, err := text(ctx, filepath.Join(root, "class/power_supply", source.Name(), "uevent"))
		if err != nil {
			return nil, err
		}
		row, err := parseSupply(source.Name(), value)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(a, b domain.PowerSupply) int { return cmp.Compare(a.ID, b.ID) })
	return rows, ctx.Err()
}
func parseSupply(id, data string) (domain.PowerSupply, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return domain.PowerSupply{}, errors.New("malformed power supply property")
		}
		if previous, exists := fields[key]; exists && previous != value {
			return domain.PowerSupply{}, errors.New("duplicate power supply property")
		}
		fields[key] = value
	}
	if fields["POWER_SUPPLY_NAME"] != id || fields["POWER_SUPPLY_TYPE"] == "" {
		return domain.PowerSupply{}, errors.New("invalid power supply identity")
	}
	row := domain.PowerSupply{ID: id, Type: fields["POWER_SUPPLY_TYPE"]}
	if value, ok := fields["POWER_SUPPLY_STATUS"]; ok {
		row.Status = &value
	}
	for _, field := range []struct {
		name   string
		target **bool
	}{{"PRESENT", &row.Present}, {"ONLINE", &row.Online}} {
		value, ok := fields["POWER_SUPPLY_"+field.name]
		if !ok {
			continue
		}
		if value != "0" && value != "1" {
			return domain.PowerSupply{}, errors.New("invalid power supply boolean")
		}
		result := value == "1"
		*field.target = &result
	}
	if value, ok := fields["POWER_SUPPLY_CAPACITY"]; ok {
		number, err := strconv.ParseUint(value, 10, 32)
		if err != nil || number > 100 {
			return domain.PowerSupply{}, errors.New("invalid battery capacity")
		}
		n := uint32(number)
		row.CapacityPercent = &n
	}
	for _, field := range []struct {
		name   string
		target **uint64
	}{
		{"ENERGY_NOW", &row.EnergyNowMicroWh}, {"ENERGY_FULL", &row.EnergyFullMicroWh},
		{"CHARGE_NOW", &row.ChargeNowMicroAh}, {"CHARGE_FULL", &row.ChargeFullMicroAh},
		{"VOLTAGE_NOW", &row.VoltageMicroV}, {"TIME_TO_EMPTY_NOW", &row.TimeToEmptySeconds}, {"TIME_TO_FULL_NOW", &row.TimeToFullSeconds},
	} {
		value, ok := fields["POWER_SUPPLY_"+field.name]
		if !ok {
			continue
		}
		number, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return domain.PowerSupply{}, err
		}
		*field.target = &number
	}
	for _, field := range []struct {
		name   string
		target **int64
	}{{"POWER_NOW", &row.PowerMicroW}, {"CURRENT_NOW", &row.CurrentMicroA}} {
		value, ok := fields["POWER_SUPPLY_"+field.name]
		if !ok {
			continue
		}
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return domain.PowerSupply{}, err
		}
		*field.target = &number
	}
	return row, nil
}
