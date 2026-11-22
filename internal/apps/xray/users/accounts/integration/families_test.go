//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/protobuf/proto"
)

func TestLiveNativeAccountFamilies(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, family := range nativeAccountFamilies() {
		t.Run(family.name, func(t *testing.T) {
			for _, transport := range []string{"http", "grpc", "nats"} {
				t.Run(transport, func(t *testing.T) {
					f := newFixture(t, pki, broker, transport)
					configureFamily(t, &f, family)
					f.config.Instances[0].Xray.WorkDirectory = t.TempDir()
					f.config.Communications.Requests.Timeout = 2 * time.Second
					if f.config.Communications.HTTP != nil {
						f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
					}
					c := startNode(t, f)
					awaitNative(t, f.native)
					exerciseFamilyMembership(t, c, transport, family)
				})
			}
		})
	}
}

func exerciseFamilyMembership(t *testing.T, c clients, transport string, family accountFamily) {
	t.Helper()
	assertFamilyCompatibility(t, c, transport, family.account)
	const email = "Managed@example.test"
	assertFamilyCount(t, c, transport, 1)
	baseline := familyMember(t, c, transport, "original@example.test")
	if baseline == nil || baseline.Account == nil || baseline.Account.Type != family.account {
		t.Fatal("original account missing", baseline)
	}
	result, code := sourceAccount(t, c, transport, "account.json", email, familySource(t, family, email, 3))
	if code != "" || result.Effect != "completed" || !result.Acknowledged || result.Email != email || result.RequiresInstanceRestart {
		t.Fatal("native source addition", result, code)
	}
	assertFamilyCount(t, c, transport, 2)
	member := familyMember(t, c, transport, email)
	if member == nil || member.Email != email || member.Level != 7 || member.Account == nil || member.Account.Type != family.account {
		t.Fatal("native readback lost account identity or type", member)
	}
	upper := familyMember(t, c, transport, strings.ToUpper(email))
	if family.caseSensitive {
		if upper != nil {
			t.Fatal("case-sensitive lookup was normalized", upper)
		}
	} else if !proto.Equal(upper, member) {
		t.Fatal("case-insensitive lookup lost the stored spelling", upper, member)
	}
	// Resubmit the exact native readback, including its concrete account payload.
	raw, err := proto.Marshal(member)
	if err != nil {
		t.Fatal(err)
	}
	result, code = accountMutation(t, c, transport, "add", "", raw)
	if family.duplicate == "rejected" {
		if code == "" || result.Acknowledged || result.Effect != "unknown" || result.RequiresInstanceRestart {
			t.Fatal("duplicate native error was misrepresented", result, code)
		}
	} else if code != "" || !result.Acknowledged || result.Effect != "completed" {
		t.Fatal("native duplicate addition was not acknowledged", result, code)
	}
	count := int64(2)
	if family.duplicate == "appended" {
		count++
	}
	assertFamilyCount(t, c, transport, count)
	result, code = accountMutation(t, c, transport, "remove", strings.ToUpper(email), nil)
	if code != "" || !result.Acknowledged || result.Effect != "completed" {
		t.Fatal("native removal", result, code)
	}
	if family.caseSensitive {
		assertFamilyCount(t, c, transport, count)
		if !proto.Equal(familyMember(t, c, transport, email), member) {
			t.Fatal("wrong-case removal changed the account")
		}
		result, code = accountMutation(t, c, transport, "remove", email, nil)
		if code != "" || !result.Acknowledged {
			t.Fatal(result, code)
		}
	}
	count--
	assertFamilyCount(t, c, transport, count)
	if family.duplicate == "appended" {
		if familyMember(t, c, transport, email) == nil {
			t.Fatal("one removal unexpectedly removed both native entries")
		}
		result, code = accountMutation(t, c, transport, "remove", email, nil)
		if code != "" || !result.Acknowledged {
			t.Fatal(result, code)
		}
	}
	assertFamilyCount(t, c, transport, 1)
	if familyMember(t, c, transport, email) != nil {
		t.Fatal("removed native account is still returned")
	}
	result, code = accountMutation(t, c, transport, "remove", email, nil)
	if family.missingRemovalOK {
		if code != "" || !result.Acknowledged || result.Effect != "completed" {
			t.Fatal("native missing removal should be acknowledged", result, code)
		}
	} else if code == "" || result.Acknowledged || result.Effect != "unknown" || result.RequiresInstanceRestart {
		t.Fatal("native missing-removal error was hidden", result, code)
	}
	if !proto.Equal(familyMember(t, c, transport, "original@example.test"), baseline) {
		t.Fatal("unselected original account changed")
	}
	assertFamilyCount(t, c, transport, 1)
	if state := accountControl(t, c, transport); state.RequestInFlight || state.RequiresInstanceRestart || state.LastOperation == nil {
		t.Fatal("terminal mutation left a false recovery fence", state)
	}
}

func assertFamilyCount(t *testing.T, c clients, transport string, want int64) {
	t.Helper()
	var result countResponse
	if err := json.Unmarshal(countData(t, c, transport), &result); err != nil || result.Count != want {
		t.Fatalf("native account count=%d want=%d: %v", result.Count, want, err)
	}
}

func familyMember(t *testing.T, c clients, transport, email string) *protocol.User {
	t.Helper()
	var result accountResponse
	if err := json.Unmarshal(accountData(t, c, transport, email), &result); err != nil {
		t.Fatal(err)
	}
	if result.RequestedEmail != email || result.Observation.Generation != 1 {
		t.Fatal("read lost request identity or generation", result)
	}
	if result.User == nil {
		return nil
	}
	var member protocol.User
	if err := proto.Unmarshal(result.User.Configuration, &member); err != nil || member.Email != result.User.Email || member.Level != result.User.Level {
		t.Fatal("native record and wire projection disagree", &member, result, err)
	}
	return &member
}
