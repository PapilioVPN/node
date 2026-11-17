package services_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/network/outbounds/domain"
)

type outboundReader func(context.Context) (domain.OutboundStatuses, error)

func (r outboundReader) ReadOutboundStatuses(ctx context.Context) (domain.OutboundStatuses, error) {
	return r(ctx)
}

func TestOutboundServiceSelectsOneInstanceAndKeepsStableDuplicates(t *testing.T) {
	ctx, observed := t.Context(), time.Unix(1700000000, 123)
	calls := 0
	bindings := map[instances.InstanceID]services.OutboundStatusReader{
		" edge ": outboundReader(func(received context.Context) (domain.OutboundStatuses, error) {
			calls++
			if received != ctx {
				t.Fatal("outbound context changed")
			}
			return domain.OutboundStatuses{Items: []domain.OutboundStatus{{Tag: "z", DelayMS: 1}, {Tag: "a", DelayMS: 2}, {Tag: "z", DelayMS: 3}, {Tag: "", HealthPing: &domain.OutboundHealthPing{}}}, ObservedAt: observed}, nil
		}),
		"disabled": nil,
		"other": outboundReader(func(context.Context) (domain.OutboundStatuses, error) {
			t.Fatal("cross-instance fetch")
			return domain.OutboundStatuses{}, nil
		}),
	}
	service := services.NewOutboundStatusService(bindings)
	delete(bindings, " edge ")
	result, err := service.ListOutboundStatuses(ctx, " edge ")
	want := []domain.OutboundStatus{{Tag: "", HealthPing: &domain.OutboundHealthPing{}}, {Tag: "a", DelayMS: 2}, {Tag: "z", DelayMS: 1}, {Tag: "z", DelayMS: 3}}
	if err != nil || calls != 1 || result.InstanceID != " edge " || result.Statuses.ObservedAt != observed || !reflect.DeepEqual(result.Statuses.Items, want) {
		t.Fatalf("collection changed identity/duplicates/order/presence: %+v %v", result, err)
	}
	for id, cause := range map[instances.InstanceID]error{"edge": instances.ErrNotFound, "disabled": domain.ErrAPIDisabled} {
		if _, err := service.ListOutboundStatuses(ctx, id); !errors.Is(err, cause) {
			t.Fatal("collection lost exact configured selection")
		}
		if _, err := service.GetOutboundStatus(ctx, id, "a"); !errors.Is(err, cause) {
			t.Fatal("single lost exact configured selection")
		}
	}
	if calls != 1 {
		t.Fatal("invalid selection made native calls")
	}
	for _, tag := range []string{"", "a", " a ", "missing", "z"} {
		before := calls
		result, err := service.GetOutboundStatus(ctx, " edge ", tag)
		if calls != before+1 {
			t.Fatal("single lookup added a preflight or per-outbound call")
		}
		if tag == "z" {
			if !errors.Is(err, domain.ErrInvalidOutboundObservation) || result != (domain.InstanceOutboundObservation{}) {
				t.Fatal("ambiguous duplicate tag became an arbitrary successful record")
			}
			continue
		}
		if err != nil || result.InstanceID != " edge " || result.Tag != tag || result.Observation.ObservedAt != observed {
			t.Fatal("single identity/observation time changed")
		}
		value := result.Observation.Status
		if tag == "" && (value == nil || value.Tag != "" || value.HealthPing == nil || value.DelayMS != 0) || tag == "a" && (value == nil || value.DelayMS != 2) || (tag == " a " || tag == "missing") && value != nil {
			t.Fatal("absent observation, anonymous tag or zero latency conflated")
		}
	}
}

func TestOutboundSingleLookupHasOneReadAtEveryCollectionSize(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			calls := 0
			service := services.NewOutboundStatusService(map[instances.InstanceID]services.OutboundStatusReader{"edge": outboundReader(func(context.Context) (domain.OutboundStatuses, error) {
				calls++
				items := make([]domain.OutboundStatus, count)
				for i := range items {
					items[i].Tag = fmt.Sprint(i)
				}
				return domain.OutboundStatuses{Items: items, ObservedAt: time.Unix(1700000000, 0)}, nil
			})})
			value, err := service.GetOutboundStatus(t.Context(), "edge", fmt.Sprint(count-1))
			if err != nil || calls != 1 || (value.Observation.Status == nil) != (count == 0) {
				t.Fatalf("single lookup fetched per outbound or fabricated absence: %+v %v", value, err)
			}
		})
	}
}

func TestOutboundServiceRejectsFailedAndCanceledPartialFacts(t *testing.T) {
	for _, cause := range []error{domain.ErrOutboundObservationUnsupported, domain.ErrAPIResourceLimit, errors.New("private cause"), nil} {
		for _, single := range []bool{false, true} {
			ctx, cancel := context.WithCancel(t.Context())
			calls := 0
			service := services.NewOutboundStatusService(map[instances.InstanceID]services.OutboundStatusReader{"edge": outboundReader(func(context.Context) (domain.OutboundStatuses, error) {
				calls++
				if cause == nil {
					cancel()
				}
				return domain.OutboundStatuses{Items: []domain.OutboundStatus{{Tag: "partial"}}, ObservedAt: time.Now()}, cause
			})})
			var err error
			if single {
				var value domain.InstanceOutboundObservation
				value, err = service.GetOutboundStatus(ctx, "edge", "partial")
				if value != (domain.InstanceOutboundObservation{}) {
					t.Fatal("failed single read returned partial facts")
				}
			} else {
				value, readError := service.ListOutboundStatuses(ctx, "edge")
				err = readError
				if value.InstanceID != "" || value.Statuses.Items != nil || !value.Statuses.ObservedAt.IsZero() {
					t.Fatal("failed collection returned partial facts")
				}
			}
			if err == nil || cause != nil && !errors.Is(err, cause) || cause == nil && !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("native/cancellation cause lost: %v", err)
			}
			cancel()
			if _, err := service.GetOutboundStatus(ctx, "edge", "partial"); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatal("pre-canceled single reached native reader")
			}
			if _, err := service.ListOutboundStatuses(ctx, "edge"); !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatal("pre-canceled collection reached native reader")
			}
		}
	}
}
