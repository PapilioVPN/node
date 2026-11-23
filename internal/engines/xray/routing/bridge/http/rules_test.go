package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
)

type standardRule struct {
	RuleTag     string `json:"rule_tag"`
	OutboundTag string `json:"outbound_tag"`
}

func writeStandardRules(w io.Writer, rows []state.Rule) {
	items := make([]standardRule, len(rows))
	for i, row := range rows {
		items[i] = standardRule(row)
	}
	_ = json.NewEncoder(w).Encode(struct {
		Rules []standardRule `json:"rules"`
	}{items})
}

func TestEncodeRulesPreservesTags(t *testing.T) {
	rows := []state.Rule{
		{RuleTag: "z-first", OutboundTag: "direct"},
		{},
		{OutboundTag: "direct"},
		{RuleTag: "unicode-فارسی\u2028", OutboundTag: "\x7f&<\r\n\x00\x1f\"\\>"},
	}
	for _, rows := range [][]state.Rule{nil, rows} {
		var standard bytes.Buffer
		writeStandardRules(&standard, rows)
		var actual bytes.Buffer
		writeRules(&actual, rows)
		if got, want := actual.Bytes(), standard.Bytes(); !slices.Equal(got, want) {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}

func BenchmarkRoutingRuleEncoding(b *testing.B) {
	for _, size := range []int{4, 512} {
		rows := make([]state.Rule, size)
		for i := range rows {
			if i%4 != 1 {
				rows[i] = state.Rule{RuleTag: fmt.Sprintf("rule-%04d", size-i), OutboundTag: "direct"}
			}
		}
		for _, method := range []struct {
			name  string
			write func(io.Writer, []state.Rule)
		}{
			{"standard", writeStandardRules},
			{"direct", func(w io.Writer, rows []state.Rule) { _, _ = w.Write(appendRules(nil, rows)) }},
			{"pooled", writeRules},
		} {
			b.Run(fmt.Sprintf("%d/%s", size, method.name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					method.write(io.Discard, rows)
				}
			})
		}
	}
}
