package sys

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func put(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTemperaturesPreserveIdentityNegativeZeroAbsenceAndReadFailures(t *testing.T) {
	root := t.TempDir()
	put(t, root, "class/hwmon/hwmon0/name", "coretemp\n")
	put(t, root, "class/hwmon/hwmon0/temp1_input", "-12500\n")
	put(t, root, "class/hwmon/hwmon0/temp1_max", "0\n")
	put(t, root, "class/hwmon/hwmon0/temp1_label", "CPU package\n")
	put(t, root, "class/hwmon/hwmon0/temp2_input", "not a number\n")
	rows, err := temperatures(context.Background(), root)
	if err == nil {
		t.Fatal("malformed kernel values must fail instead of becoming zero")
	}
	if len(rows) != 0 {
		t.Fatal("failed observation returned partial values")
	}
	put(t, root, "class/hwmon/hwmon0/temp2_input", "0\n")
	rows, err = temperatures(context.Background(), root)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	if rows[0].ID != "hwmon0/temp1" || rows[0].MilliCelsius != -12500 || rows[0].HighMilliCelsius == nil || *rows[0].HighMilliCelsius != 0 || rows[0].CriticalMilliCelsius != nil || rows[0].Label == nil || *rows[0].Label != "CPU package" || rows[1].MilliCelsius != 0 {
		t.Fatalf("lost native identity/presence: %+v", rows)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := temperatures(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestThermalFallbackAndIndependentFans(t *testing.T) {
	root := t.TempDir()
	put(t, root, "class/hwmon/hwmon0/name", "fan-controller")
	put(t, root, "class/hwmon/hwmon0/fan1_input", "0")
	put(t, root, "class/thermal/thermal_zone0/type", "acpitz")
	put(t, root, "class/thermal/thermal_zone0/temp", "31000")
	rows, err := temperatures(context.Background(), root)
	if err != nil || len(rows) != 1 || rows[0].ID != "thermal_zone0" || rows[0].MilliCelsius != 31000 {
		t.Fatalf("%+v %v", rows, err)
	}
	// Fan reads must not parse temperature attributes.
	put(t, root, "class/thermal/thermal_zone0/temp", "invalid")
	fans, err := fans(context.Background(), root)
	if err != nil || len(fans) != 1 || fans[0].RPM != 0 {
		t.Fatalf("%+v %v", fans, err)
	}
}

func TestPowerSupplyUeventPrecisionPresenceAndValidation(t *testing.T) {
	data := "POWER_SUPPLY_NAME=BAT0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_STATUS=Discharging\nPOWER_SUPPLY_PRESENT=0\nPOWER_SUPPLY_CAPACITY=0\nPOWER_SUPPLY_ENERGY_NOW=9007199254740993\nPOWER_SUPPLY_CURRENT_NOW=-123\nPOWER_SUPPLY_TIME_TO_EMPTY_NOW=0\n"
	row, err := parseSupply("BAT0", data)
	if err != nil || row.EnergyNowMicroWh == nil || *row.EnergyNowMicroWh != 9007199254740993 || row.Present == nil || *row.Present || row.CapacityPercent == nil || *row.CapacityPercent != 0 || row.CurrentMicroA == nil || *row.CurrentMicroA != -123 || row.TimeToEmptySeconds == nil || *row.TimeToEmptySeconds != 0 || row.Online != nil {
		t.Fatalf("%+v %v", row, err)
	}
	for _, invalid := range []string{data + "POWER_SUPPLY_CAPACITY=101\n", "POWER_SUPPLY_NAME=OTHER\nPOWER_SUPPLY_TYPE=Battery\n", "POWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_PRESENT=2\n"} {
		if _, err := parseSupply("BAT0", invalid); err == nil {
			t.Fatal("invalid native record accepted")
		}
	}
}

func TestLegacyHwmonAndUeventOnlyPowerSupply(t *testing.T) {
	root := t.TempDir()
	put(t, root, "class/hwmon/hwmon3/device/name", "legacy")
	put(t, root, "class/hwmon/hwmon3/device/temp1_input", "12000")
	rows, err := temperatures(context.Background(), root)
	if err != nil || len(rows) != 1 || rows[0].ID != "hwmon3/temp1" {
		t.Fatalf("%+v %v", rows, err)
	}
	put(t, root, "class/power_supply/BAT0/uevent", "POWER_SUPPLY_NAME=BAT0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_CAPACITY=25\n")
	power, err := powerSupplies(context.Background(), root)
	if err != nil || len(power) != 1 || power[0].CapacityPercent == nil || *power[0].CapacityPercent != 25 {
		t.Fatalf("%+v %v", power, err)
	}
	put(t, root, "class/power_supply/BAT0/uevent", string(make([]byte, (64<<10)+1)))
	if _, err := powerSupplies(context.Background(), root); err == nil {
		t.Fatal("oversized source accepted")
	}
}

func FuzzPowerSupply(f *testing.F) {
	f.Add("POWER_SUPPLY_NAME=BAT0\nPOWER_SUPPLY_TYPE=Battery\nPOWER_SUPPLY_CAPACITY=0\n")
	f.Add("")
	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 64<<10 {
			return
		}
		row, err := parseSupply("BAT0", data)
		if err == nil && (row.ID != "BAT0" || row.Type == "" || row.CapacityPercent != nil && *row.CapacityPercent > 100) {
			t.Fatal("invalid accepted identity/capacity")
		}
	})
}
