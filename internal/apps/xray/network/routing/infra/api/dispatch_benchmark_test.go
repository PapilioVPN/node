package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
	"github.com/xtls/xray-core/app/router"
	"github.com/xtls/xray-core/common/geodata"
	"google.golang.org/protobuf/proto"
)

// BenchmarkPreparedRuleDispatch measures the adapter's local work on an already
// compiled rule. The controlled connection performs no network or engine I/O.
func BenchmarkPreparedRuleDispatch(b *testing.B) {
	for _, domains := range []int{1, 1000, 16000} {
		b.Run(fmt.Sprint(domains), func(b *testing.B) {
			rule := &router.RoutingRule{RuleTag: "prepared", TargetTag: &router.RoutingRule_Tag{Tag: "direct"}, Domain: make([]*geodata.DomainRule, domains)}
			for i := range rule.Domain {
				rule.Domain[i] = &geodata.DomainRule{Value: &geodata.DomainRule_Custom{Custom: &geodata.Domain{Type: geodata.Domain_Full, Value: fmt.Sprintf("target-%d.example.invalid", i)}}}
			}
			payload, err := proto.Marshal(&router.Config{Rule: []*router.RoutingRule{rule}})
			if err != nil {
				b.Fatal(err)
			}
			connection := &connection{call: func(string, any, any) error { return nil }}
			adapter := New(connection)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				result, err := adapter.Add(context.Background(), payload, domain.Append)
				if err != nil || !result.Acknowledged {
					b.Fatal(result, err)
				}
			}
		})
	}
}
