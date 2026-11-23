package http

import (
	"encoding/json"
	"io"
	"slices"
	"sync"
	"unicode/utf8"

	"github.com/PapilioVPN/node/internal/engines/xray/routing/state"
)

const maxReusableRulesBuffer = 64 << 10

var rulesBuffers = sync.Pool{New: func() any { return new([]byte) }}

func writeRules(w io.Writer, rules []state.Rule) {
	buffer := rulesBuffers.Get().(*[]byte)
	body := appendRules((*buffer)[:0], rules)
	_, _ = w.Write(body)
	if cap(body) <= maxReusableRulesBuffer {
		*buffer = body[:0]
	} else {
		*buffer = nil
	}
	rulesBuffers.Put(buffer)
}

func appendRules(body []byte, rules []state.Rule) []byte {
	const (
		start       = `{"rules":[`
		ruleTag     = `{"rule_tag":`
		outboundTag = `,"outbound_tag":`
		end         = "]}\n"
		rowOverhead = len(ruleTag) + len(outboundTag) + len(`""""},`)
	)
	size := len(start) + len(end) + rowOverhead*len(rules)
	for _, rule := range rules {
		size += len(rule.RuleTag) + len(rule.OutboundTag)
	}

	body = slices.Grow(body, size)
	body = append(body, start...)
	for i, rule := range rules {
		if i != 0 {
			body = append(body, ',')
		}
		body = append(body, ruleTag...)
		body = appendTag(body, rule.RuleTag)
		body = append(body, outboundTag...)
		body = appendTag(body, rule.OutboundTag)
		body = append(body, '}')
	}
	return append(body, end...)
}

func appendTag(body []byte, tag string) []byte {
	for i := 0; i < len(tag); i++ {
		switch c := tag[i]; {
		case c < ' ', c >= utf8.RuneSelf, c == '"', c == '\\', c == '<', c == '>', c == '&':
			// Keep the standard encoder's escaping for arbitrary Xray tags.
			quoted, _ := json.Marshal(tag)
			return append(body, quoted...)
		}
	}
	body = append(body, '"')
	body = append(body, tag...)
	return append(body, '"')
}
