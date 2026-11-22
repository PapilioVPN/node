package jsonwire

import (
	"encoding/json"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/network/routing/domain"
)

type Operation struct {
	InstanceID      string        `json:"instance_id"`
	Kind            string        `json:"kind"`
	Generation      uint64        `json:"generation,string"`
	Effect          domain.Effect `json:"effect"`
	Acknowledged    bool          `json:"acknowledged"`
	RequiresRestart bool          `json:"requires_instance_restart"`
	ObservedAt      time.Time     `json:"observed_at"`
	DurationNS      int64         `json:"duration_ns,string"`
}

func operation(o domain.Operation) Operation {
	return Operation{o.InstanceID, o.Kind, o.Generation, o.Effect, o.Acknowledged, o.RequiresInstanceRestart, o.ObservedAt, int64(o.Duration)}
}

type Observation struct {
	InstanceID string    `json:"instance_id"`
	Generation uint64    `json:"generation,string"`
	ObservedAt time.Time `json:"observed_at"`
	DurationNS int64     `json:"duration_ns,string"`
	Source     string    `json:"source"`
}

func observation(o domain.Observation, source string) Observation {
	return Observation{o.InstanceID, o.Generation, o.ObservedAt, int64(o.Duration), source}
}
func EncodeOperation(o domain.Operation, err error) []byte {
	data, _ := json.Marshal(struct {
		Operation Operation `json:"operation"`
		Error     string    `json:"error,omitempty"`
	}{operation(o), ErrorCode(err)})
	return data
}
func EncodeProbe(p domain.ProbeResult, err error) []byte {
	data, _ := json.Marshal(struct {
		Operation       Operation `json:"operation"`
		Route           *Context  `json:"route"`
		TrafficVerified bool      `json:"traffic_verified"`
		Error           string    `json:"error,omitempty"`
	}{operation(p.Operation), (*Context)(p.Route), false, ErrorCode(err)})
	return data
}
func EncodeBalancer(b domain.BalancerResult) []byte {
	data, _ := json.Marshal(struct {
		Observation Observation `json:"observation"`
		Tag         string      `json:"tag"`
		Override    *string     `json:"override"`
		Principal   *[]string   `json:"principal_targets"`
	}{observation(b.Observation, "xray.routing.GetBalancerInfo"), b.Tag, b.Balancer.Override, b.Balancer.PrincipalTargets})
	return data
}

type Rule struct {
	OutboundTag string `json:"outbound_tag"`
	RuleTag     string `json:"rule_tag"`
}

func EncodePage(p domain.Page) []byte {
	rows := make([]Rule, len(p.Rules))
	for i, r := range p.Rules {
		rows[i] = Rule(r)
	}
	data, _ := json.Marshal(struct {
		Observation   Observation `json:"observation"`
		Rules         []Rule      `json:"rules"`
		ObservationID string      `json:"observation_id"`
		Next          string      `json:"next_page_token"`
		ExpiresAt     time.Time   `json:"expires_at"`
		ServedAt      time.Time   `json:"served_at"`
		Total         int         `json:"total"`
		Fresh         bool        `json:"fresh"`
	}{observation(p.Observation, "xray.routing.ListRule"), rows, p.ObservationID, p.NextPageToken, p.ExpiresAt, p.ServedAt, p.Total, p.Fresh})
	return data
}
func EncodeStatus(s domain.ControlStatus) []byte {
	var last *Operation
	if s.LastOperation != nil {
		v := operation(*s.LastOperation)
		last = &v
	}
	data, _ := json.Marshal(struct {
		Observation Observation `json:"observation"`
		State       string      `json:"execution_state"`
		Configured  bool        `json:"api_configured"`
		Active      bool        `json:"request_in_flight"`
		Restart     bool        `json:"requires_instance_restart"`
		Last        *Operation  `json:"last_operation"`
		Stream      string      `json:"routing_stats_stream"`
	}{observation(s.Observation, "node.routing.control"), s.ExecutionState, s.APIConfigured, s.RequestInFlight, s.RequiresInstanceRestart, last, s.RoutingStatsStream})
	return data
}
