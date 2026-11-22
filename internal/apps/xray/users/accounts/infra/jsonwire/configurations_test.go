package jsonwire_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/jsonwire"
)

func TestConfigurationPreparationRequiresExplicitAccountArrays(t *testing.T) {
	input, err := jsonwire.DecodePreparation([]byte(`{"sources":[{"name":"base.json","content":"e30="}],"inbounds":[{"inbound_tag":"a","accounts":[]}]}`))
	if err != nil || len(input.Inbounds) != 1 || len(input.Inbounds[0].Accounts) != 0 {
		t.Fatal(input, err)
	}
	for _, body := range []string{
		`null`, `{}`, `{"sources":[],"inbounds":null}`,
		`{"sources":[],"inbounds":[{"inbound_tag":"a"}]}`,
		`{"sources":[],"inbounds":[{"inbound_tag":"a","accounts":null}]}`,
		`{"sources":[],"inbounds":[{"inbound_tag":null,"accounts":[]}]}`,
		`{"sources":[],"inbounds":[{"inbound_tag":"a","accounts":[null]}]}`,
		`{"sources":[],"inbounds":[{"inbound_tag":"a","accounts":[],"accounts":[]}]}`,
		`{"sources":[],"inbounds":[],"sources":[]}`,
		`{"sources":[],"inbounds":[],"instance_id":null}`,
		`{"sources":[],"inbounds":[],"extra":true}`,
		`{"sources":[],"inbounds":[]} {}`,
	} {
		if _, err := jsonwire.DecodePreparation([]byte(body)); err == nil {
			t.Fatal(body)
		}
	}
}

func FuzzAccountConfigurationEnvelope(f *testing.F) {
	f.Add([]byte(`{"sources":[],"inbounds":[{"inbound_tag":"a","accounts":[]}]}`))
	f.Add([]byte(`{"sources":null,"inbounds":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if _, err := jsonwire.DecodePreparation(data); err == nil && !json.Valid(data) {
			t.Fatal("accepted invalid JSON")
		}
	})
}

func TestConfigurationEnvelopeBoundsAccountsAcrossAllInbounds(t *testing.T) {
	accounts := strings.Repeat(`"AQ==",`, domain.MaxConfigurationUsers/2) + `"AQ=="`
	body := `{"sources":[],"inbounds":[{"inbound_tag":"a","accounts":[` + accounts + `]},{"inbound_tag":"b","accounts":[` + accounts + `]}]}`
	if _, err := jsonwire.DecodePreparation([]byte(body)); !errors.Is(err, domain.ErrCapacity) {
		t.Fatal(err)
	}
}
