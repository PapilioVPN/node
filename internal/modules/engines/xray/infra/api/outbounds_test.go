package api

import (
	"errors"
	"reflect"
	"testing"

	"github.com/xtls/xray-core/app/observatory"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
)

func TestOutboundObservationRejectsMissingAndUnrepresentedRecords(t *testing.T) {
	if value, err := observedOutbound(nil); !errors.Is(err, domain.ErrInvalidOutboundObservation) || !reflect.DeepEqual(value, domain.OutboundStatus{}) {
		t.Fatal("missing native record became zero latency")
	}
	unknown := protowire.AppendVarint(protowire.AppendTag(nil, 200, protowire.VarintType), 1)
	for _, healthExtension := range []bool{false, true} {
		value := &observatory.OutboundStatus{OutboundTag: "partial", HealthPing: &observatory.HealthPingMeasurementResult{}}
		if healthExtension {
			value.HealthPing.ProtoReflect().SetUnknown(unknown)
		} else {
			value.ProtoReflect().SetUnknown(unknown)
		}
		if result, err := observedOutbound(value); !errors.Is(err, domain.ErrOutboundObservationUnsupported) || !reflect.DeepEqual(result, domain.OutboundStatus{}) {
			t.Fatal("unrepresented native fields became partial/successful facts")
		}
	}
	for _, health := range []*observatory.HealthPingMeasurementResult{nil, {}} {
		value, err := observedOutbound(&observatory.OutboundStatus{HealthPing: health})
		if err != nil || value.Tag != "" || value.DelayMS != 0 || (value.HealthPing == nil) != (health == nil) {
			t.Fatal("anonymous/zero observation or empty health presence was lost")
		}
	}
}
