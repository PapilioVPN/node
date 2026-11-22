package configs

import (
	"testing"

	upstream "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	accounts "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

func TestApplicationEvidenceKeepsPublicationReadinessAndRecoverySeparate(t *testing.T) {
	a := upstream.Application{Revision: 2, Generation: 4, Outcome: "completed", EngineEffect: "completed", Readiness: "api_ready", Persistence: upstream.Persistence{Durable: true, Published: true}, ReceiptDurable: true}
	r := result(a)
	if r.Effect != accounts.Completed || !r.Ready || !r.Durable {
		t.Fatal(r)
	}
	a.Outcome = "failed"
	a.Readiness = "unavailable"
	a.RequiresRecovery = true
	r = result(a)
	if r.Effect != accounts.Partial || r.Ready || !r.Durable || !r.RequiresRecovery {
		t.Fatal(r)
	}
	a.EngineEffect = "unknown"
	r = result(a)
	if r.Effect != accounts.Unknown {
		t.Fatal(r)
	}
	a.EngineEffect = "not_attempted"
	a.Persistence.PublicationUnknown = true
	r = result(a)
	if r.Effect != accounts.Unknown {
		t.Fatal("lost publication acknowledgement", r)
	}
	a.Persistence.PublicationUnknown = false
	a.Recovery = &upstream.Recovery{Revision: 3, Generation: 5, Outcome: "completed", EngineEffect: "completed", Readiness: "api_ready", Persistence: upstream.Persistence{Durable: true, Published: true}}
	a.RequiresRecovery = false
	r = result(a)
	if r.Revision != 3 || r.Generation != 5 || r.RequiresRecovery || !r.Ready || r.Effect != accounts.Completed {
		t.Fatal(r)
	}
}
