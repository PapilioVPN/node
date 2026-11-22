package jsonwire

import (
	"github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/domain"
	"time"
)

type Summary struct {
	Tag           string `json:"tag"`
	Direction     string `json:"direction"`
	Protocol      string `json:"protocol"`
	NativeType    string `json:"native_type"`
	AccountType   string `json:"account_type"`
	LiveAccounts  bool   `json:"live_accounts"`
	Reason        string `json:"reason"`
	EmailMatching string `json:"email_matching"`
}

func summaryResult(s domain.Summary) Summary {
	return Summary{Tag: s.Tag, Direction: s.Direction, Protocol: s.Protocol, NativeType: s.NativeType, AccountType: s.AccountType, LiveAccounts: s.LiveAccounts, Reason: s.Reason, EmailMatching: s.EmailMatching}
}
func summariesResult(items []domain.Summary) []Summary {
	result := make([]Summary, len(items))
	for i, s := range items {
		result[i] = summaryResult(s)
	}
	return result
}

type ReplacementPlan struct {
	Token               string    `json:"token"`
	InstanceID          string    `json:"instance_id"`
	Generation          uint64    `json:"generation,string"`
	Tag                 string    `json:"tag"`
	CurrentSHA256       string    `json:"current_sha256"`
	CandidateSHA256     string    `json:"candidate_sha256"`
	Mode                string    `json:"mode"`
	Reason              string    `json:"reason"`
	ExistingConnections string    `json:"existing_connections"`
	NewConnections      string    `json:"new_connections"`
	Fallback            string    `json:"fallback"`
	ExpiresAt           time.Time `json:"expires_at"`
}

func PlanResult(p domain.ReplacementPlan) ReplacementPlan {
	return ReplacementPlan{Token: p.Token, InstanceID: p.InstanceID, Generation: p.Generation, Tag: p.Tag, CurrentSHA256: p.CurrentSHA256, CandidateSHA256: p.CandidateSHA256, Mode: p.Mode, Reason: p.Reason, ExistingConnections: p.ExistingConnections, NewConnections: p.NewConnections, Fallback: p.Fallback, ExpiresAt: p.ExpiresAt}
}

type ReplacementStep struct {
	Attempted    bool          `json:"attempted"`
	Effect       domain.Effect `json:"effect"`
	Acknowledged bool          `json:"acknowledged"`
}
type ReplacementProgress struct {
	Token      string          `json:"token"`
	Remove     ReplacementStep `json:"remove"`
	Add        ReplacementStep `json:"add"`
	NextAction string          `json:"next_action"`
}

func ProgressResult(p *domain.ReplacementProgress) *ReplacementProgress {
	if p == nil {
		return nil
	}
	return &ReplacementProgress{Token: p.Token, Remove: ReplacementStep{Attempted: p.Remove.Attempted, Effect: p.Remove.Effect, Acknowledged: p.Remove.Acknowledged}, Add: ReplacementStep{Attempted: p.Add.Attempted, Effect: p.Add.Effect, Acknowledged: p.Add.Acknowledged}, NextAction: p.NextAction}
}
