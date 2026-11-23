package liveupdate

import (
	"slices"
	"testing"
)

func TestOutboundAndRoutingPlanOrder(t *testing.T) {
	previous := []byte(`{
		"outbounds":[{"tag":"default","protocol":"freedom"},{"tag":"next","protocol":"freedom"},
			{"tag":"edit","protocol":"freedom"},{"tag":"obsolete","protocol":"blackhole"}],
		"routing":{"rules":[{"inboundTag":["a"],"outboundTag":"obsolete"}]}
	}`)
	candidate := []byte(`{
		"outbounds":[{"tag":"next","protocol":"freedom"},{"tag":"default","protocol":"freedom"},
			{"tag":"new","protocol":"freedom"},{"tag":"edit","protocol":"blackhole"}],
		"routing":{"rules":[{"inboundTag":["a"],"outboundTag":"new"}]}
	}`)
	plan, ok := BuildPlan(previous, candidate)
	want := []step{
		{removeOutbound, "default"}, {removeOutbound, "next"}, {addOutbound, "next"},
		{addOutbound, "default"}, {addOutbound, "new"}, {removeOutbound, "edit"},
		{addOutbound, "edit"}, {replaceRouting, ""}, {removeOutbound, "obsolete"},
	}
	if !ok || !slices.Equal(plan.steps, want) {
		t.Fatalf("BuildPlan = (%+v, %t), want %+v", plan.steps, ok, want)
	}
}

func TestPlanRejectsChangesTheNativeCommandsCannotSafelyApply(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
	}{
		{"inbound", `{"inbounds":[]}`, `{"inbounds":[{"tag":"in"}]}`},
		{"domain strategy", `{"routing":{"domainStrategy":"AsIs"}}`, `{"routing":{"domainStrategy":"IPIfNonMatch"}}`},
		{"router installation", `{}`, `{"routing":{"rules":[]}}`},
		{"untagged outbound", `{"outbounds":[{"protocol":"freedom"}]}`, `{"outbounds":[{"tag":"next","protocol":"freedom"}]}`},
		{"duplicate configuration field", `{"routing":{},"ROUTING":{"rules":[]}}`, `{"routing":{"rules":[]}}`},
		{"wireguard removal", `{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"wg","protocol":"wireguard"}]}`,
			`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`},
		{"active mux removal", `{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"vmess","protocol":"vmess","mux":{"enabled":true}}]}`,
			`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`},
		{"active vless reverse removal",
			`{"outbounds":[{"tag":"direct","protocol":"freedom"},{"tag":"rev","protocol":"vless","settings":{"reverse":{"tag":"r"}}}]}`,
			`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if plan, ok := BuildPlan([]byte(tc.before), []byte(tc.after)); ok {
				t.Fatalf("BuildPlan allowed live changes: %+v", plan.steps)
			}
		})
	}
}
