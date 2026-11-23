// Package liveupdate plans and prepares supported changes to a running Xray configuration.
package liveupdate

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"strings"
	"unicode"

	xrayjson "github.com/xtls/xray-core/infra/conf/json"
)

const maxOperations = 256

type operationKind uint8

const (
	addOutbound operationKind = iota
	removeOutbound
	replaceRouting
)

type step struct {
	kind operationKind
	tag  string
}

type Plan struct{ steps []step }

type sourceParts struct {
	base      fields
	router    fields
	outbounds json.RawMessage
	rules     json.RawMessage
	balancers json.RawMessage
}

func BuildPlan(current, candidate []byte) (Plan, bool) {
	old, okOld := split(current)
	next, okNext := split(candidate)
	if !okOld || !okNext || !old.base.equal(next.base) ||
		(old.router == nil) != (next.router == nil) || !old.router.equal(next.router) {
		return Plan{}, false
	}

	var steps, retired []step
	if !bytes.Equal(old.outbounds, next.outbounds) {
		// The pinned outbound manager can race with its selector cache when
		// handlers change. Balancers and observatories can call Select.
		if hasEntries(old.balancers) || hasEntries(next.balancers) ||
			old.base["observatory"] != nil || old.base["burstobservatory"] != nil {
			return Plan{}, false
		}
		before, okOld := readOutbounds(old.outbounds)
		after, okNext := readOutbounds(next.outbounds)
		if !okOld || !okNext {
			return Plan{}, false
		}
		var ok bool
		steps, retired, ok = planOutbounds(before, after)
		if !ok {
			return Plan{}, false
		}
	}

	if !bytes.Equal(old.rules, next.rules) || !bytes.Equal(old.balancers, next.balancers) {
		// Native AddRule replaces only rules and balancers. A routing script
		// bypasses the rules, and a router without rules cannot be added live.
		if old.router == nil || old.router["script"] != nil {
			return Plan{}, false
		}
		steps = append(steps, step{kind: replaceRouting})
	}
	// Deletions of obsolete handlers run after routing stops referring to them.
	steps = append(steps, retired...)
	return Plan{steps: steps}, len(steps) <= maxOperations
}

func split(source []byte) (sourceParts, bool) {
	decoder := json.NewDecoder(&xrayjson.Reader{Reader: bytes.NewReader(source)})
	base, ok := objectFields(decoder)
	if !ok {
		return sourceParts{}, false
	}
	result := sourceParts{base: base, outbounds: base.take("outbounds")}
	if raw := base.take("routing"); raw != nil {
		result.router, ok = objectFields(json.NewDecoder(bytes.NewReader(raw)))
		if !ok {
			return sourceParts{}, false
		}
		result.rules = result.router.take("rules")
		result.balancers = result.router.take("balancers")
	}
	return result, true
}

// fields holds opaque Xray fields for comparison. Repeated case-insensitive
// keys are ineligible because decoding can merge objects or replace scalars.
type fields map[string]json.RawMessage

func objectFields(decoder *json.Decoder) (fields, bool) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	result := make(fields)
	var compact bytes.Buffer
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		name, ok := token.(string)
		if !ok || strings.IndexFunc(name, func(r rune) bool { return r > unicode.MaxASCII }) >= 0 {
			return nil, false
		}
		key := strings.ToLower(name)
		if _, duplicate := result[key]; duplicate {
			return nil, false
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, false
		}
		compact.Reset()
		if err := json.Compact(&compact, raw); err != nil {
			return nil, false
		}
		result[key] = bytes.Clone(compact.Bytes())
	}
	if _, err := decoder.Token(); err != nil || decoder.Decode(new(json.RawMessage)) != io.EOF {
		return nil, false
	}
	return result, true
}

func (f fields) take(key string) json.RawMessage {
	value := f[key]
	delete(f, key)
	return value
}

func (f fields) equal(other fields) bool {
	return maps.EqualFunc(f, other, func(a, b json.RawMessage) bool { return bytes.Equal(a, b) })
}

func hasEntries(raw json.RawMessage) bool {
	return len(raw) > 0 && !bytes.Equal(raw, []byte("[]")) && !bytes.Equal(raw, []byte("null"))
}

type outbound struct {
	Tag      string          `json:"tag"`
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
	Mux      *struct {
		Enabled bool `json:"enabled"`
	} `json:"mux"`
	raw json.RawMessage
}

type outbounds struct {
	order []string
	byTag map[string]outbound
}

func readOutbounds(data json.RawMessage) (outbounds, bool) {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || len(raw) == 0 {
		return outbounds{}, false
	}
	result := outbounds{byTag: make(map[string]outbound, len(raw))}
	for _, item := range raw {
		var entry outbound
		if err := json.Unmarshal(item, &entry); err != nil || entry.Tag == "" {
			return outbounds{}, false
		}
		if _, duplicate := result.byTag[entry.Tag]; duplicate {
			return outbounds{}, false
		}
		entry.raw = item
		result.byTag[entry.Tag] = entry
		result.order = append(result.order, entry.Tag)
	}
	return result, true
}

// Xray's RemoveOutbound drops the handler without closing it. Do not remove
// handlers with independent connection pools, reverse links, or background I/O.
func (o outbound) removable() bool {
	if o.Mux != nil && o.Mux.Enabled {
		return false
	}
	switch strings.ToLower(o.Protocol) {
	case "freedom", "direct", "blackhole", "block", "loopback", "dns",
		"http", "socks", "shadowsocks", "vmess", "trojan", "hysteria":
		return true
	case "vless":
		var settings struct {
			Reverse json.RawMessage `json:"reverse"`
			Testpre uint32          `json:"testpre"`
			Vnext   []struct {
				Users []struct {
					Reverse json.RawMessage `json:"reverse"`
					Testpre uint32          `json:"testpre"`
				} `json:"users"`
			} `json:"vnext"`
		}
		if json.Unmarshal(o.Settings, &settings) != nil || hasEntries(settings.Reverse) || settings.Testpre != 0 {
			return false
		}
		for _, server := range settings.Vnext {
			for _, user := range server.Users {
				if hasEntries(user.Reverse) || user.Testpre != 0 {
					return false
				}
			}
		}
		return true
	default:
		return false
	}
}

func planOutbounds(old, next outbounds) (steps []step, retired []step, compatible bool) {
	oldDefault, newDefault := old.order[0], next.order[0]
	if oldDefault != newDefault {
		if !old.byTag[oldDefault].removable() {
			return nil, nil, false
		}
		steps = append(steps, step{removeOutbound, oldDefault})
		if handler, present := old.byTag[newDefault]; present {
			if !handler.removable() {
				return nil, nil, false
			}
			steps = append(steps, step{removeOutbound, newDefault})
		}
		steps = append(steps, step{addOutbound, newDefault})
		if _, kept := next.byTag[oldDefault]; kept {
			steps = append(steps, step{addOutbound, oldDefault})
		}
	}

	for _, tag := range next.order {
		if oldDefault != newDefault && (tag == oldDefault || tag == newDefault) {
			continue
		}
		before, existed := old.byTag[tag]
		if !existed {
			steps = append(steps, step{addOutbound, tag})
		} else if !bytes.Equal(before.raw, next.byTag[tag].raw) {
			if !before.removable() {
				return nil, nil, false
			}
			steps = append(steps, step{removeOutbound, tag}, step{addOutbound, tag})
		}
	}
	for _, tag := range old.order {
		if _, kept := next.byTag[tag]; kept || (oldDefault != newDefault && tag == oldDefault) {
			continue
		}
		if !old.byTag[tag].removable() {
			return nil, nil, false
		}
		retired = append(retired, step{removeOutbound, tag})
	}
	return steps, retired, true
}
