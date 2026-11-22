package jsonwire

import (
	"encoding/json"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

type Metadata struct {
	Email       string `json:"email"`
	Level       uint32 `json:"level"`
	AccountType string `json:"account_type"`
}
type Guidance struct {
	Reason              string `json:"reason"`
	NextAction          string `json:"next_action"`
	Protocol            string `json:"protocol"`
	ExpectedAccountType string `json:"expected_account_type"`
	ActualAccountType   string `json:"actual_account_type"`
}

func guidanceResult(g *domain.Guidance) *Guidance {
	if g == nil {
		return nil
	}
	return &Guidance{g.Reason, g.NextAction, g.Protocol, g.ExpectedAccountType, g.ActualAccountType}
}
func safeMetadata(m domain.Metadata) Metadata { return Metadata{m.Email, m.Level, m.AccountType} }

type ReadCost struct {
	NativeBytes      uint64 `json:"native_payload_bytes,string"`
	NativeDurationNS int64  `json:"native_duration_ns,string"`
	NativeLimitBytes uint64 `json:"native_limit_bytes,string"`
}

func readCost(c domain.ReadCost) ReadCost {
	return ReadCost{uint64(c.NativeBytes), int64(c.NativeDuration), domain.MaxCollectionBytes}
}
func EncodeMetadataPage(p domain.MetadataPage) ([]byte, error) {
	users := make([]Metadata, len(p.Users))
	for i, u := range p.Users {
		users[i] = safeMetadata(u)
	}
	return json.Marshal(struct {
		Observation observation `json:"observation"`
		Users       []Metadata  `json:"users"`
		NextCursor  string      `json:"next_cursor"`
		ExpiresAt   time.Time   `json:"expires_at"`
		ReadCost    ReadCost    `json:"read_cost"`
	}{metadata(p.Observation), users, p.NextCursor, p.ExpiresAt, readCost(p.ReadCost)})
}
func EncodeExport(p domain.AccountPage) ([]byte, error) {
	type account struct {
		Email         string `json:"email"`
		Level         uint32 `json:"level"`
		Configuration []byte `json:"configuration"`
	}
	users := make([]account, len(p.Users))
	for i, u := range p.Users {
		users[i] = account{u.Email, u.Level, u.Configuration}
	}
	return json.Marshal(struct {
		Observation observation `json:"observation"`
		Users       []account   `json:"users"`
		NextCursor  string      `json:"next_cursor"`
		ExpiresAt   time.Time   `json:"expires_at"`
		ReadCost    ReadCost    `json:"read_cost"`
	}{metadata(p.Observation), users, p.NextCursor, p.ExpiresAt, readCost(p.ReadCost)})
}
func EncodeMetadata(p domain.MetadataAccount) ([]byte, error) {
	var user *Metadata
	if p.User != nil {
		u := safeMetadata(*p.User)
		user = &u
	}
	return json.Marshal(struct {
		Observation    observation `json:"observation"`
		RequestedEmail string      `json:"requested_email"`
		User           *Metadata   `json:"user"`
	}{metadata(p.Observation), p.RequestedEmail, user})
}
func EncodeCompatibility(p domain.Compatibility) ([]byte, error) {
	return json.Marshal(struct {
		Observation          observation `json:"observation"`
		Protocol             string      `json:"protocol"`
		ExpectedAccountType  string      `json:"expected_account_type"`
		LiveAccounts         bool        `json:"live_accounts"`
		EmailMatching        string      `json:"email_matching"`
		RequestedAccountType *string     `json:"requested_account_type"`
		Compatible           *bool       `json:"compatible"`
		Guidance             *Guidance   `json:"guidance"`
	}{metadata(p.Observation), p.Inbound.Protocol, p.Inbound.AccountType, p.Inbound.LiveAccounts, p.Inbound.EmailMatching, p.RequestedAccountType, p.Compatible, guidanceResult(&p.Guidance)})
}
