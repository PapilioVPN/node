package traffic

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

type linkReaderFunc func(context.Context, string) (LinkTrafficSample, error)

func (read linkReaderFunc) ReadInterfaceTraffic(ctx context.Context, name string) (LinkTrafficSample, error) {
	return read(ctx, name)
}

func TestInterfaceUsageBaseline(t *testing.T) {
	const name = "wg-test"
	sample := LinkTrafficSample{Counters: Counters{RXBytes: 100, TXBytes: 300}, Index: 1, Alias: "test-link"}
	service := New(name, linkReaderFunc(func(context.Context, string) (LinkTrafficSample, error) {
		return sample, nil
	}), nil, true)
	read := func(want Counters) {
		t.Helper()
		if got, err := service.GetInterfaceUsage(t.Context(), name); err != nil || got != want {
			t.Fatalf("usage = %+v, %v, want %+v", got, err, want)
		}
	}
	read(Counters{})
	sample.Counters = Counters{RXBytes: 125, TXBytes: 360}
	want := Counters{RXBytes: 25, TXBytes: 60}
	read(want)
	read(want)
	if got, err := service.ResetInterfaceUsage(t.Context(), name); err != nil || got != want {
		t.Fatalf("reset = %+v, %v, want %+v", got, err, want)
	}
	read(Counters{})
	sample.Counters = Counters{RXBytes: 400, TXBytes: 500}
	sample.Index++
	read(Counters{})
	sample.TXBytes--
	read(Counters{})
	sample.RXBytes += 19
	read(Counters{RXBytes: 19})
}

func TestInterfaceUsageRejectsRemovedLinkSample(t *testing.T) {
	const name = "wg-test"
	var service *Service
	service = New(name, linkReaderFunc(func(context.Context, string) (LinkTrafficSample, error) {
		service.ForgetLinkTraffic()
		return LinkTrafficSample{Index: 1}, nil
	}), nil, true)
	if _, err := service.ResetInterfaceUsage(t.Context(), name); !errors.Is(err, status.ErrBusy) {
		t.Fatalf("removed link reset returned %v", err)
	}
	if service.linkBase.Index != 0 {
		t.Fatal("removed link reset changed the baseline")
	}
}

func TestInterfaceUsageResetRequiresManagedMode(t *testing.T) {
	service := New("wg-test", nil, nil, false)
	if _, err := service.ResetInterfaceUsage(t.Context(), "wg-test"); !errors.Is(err, ErrLinkResetDisabled) {
		t.Fatalf("read-only reset returned %v", err)
	}
}
