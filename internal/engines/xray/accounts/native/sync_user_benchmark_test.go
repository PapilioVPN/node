package native

import (
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"google.golang.org/grpc"
)

func BenchmarkSyncUserPhases(b *testing.B) {
	layout := accountLayouts[vlessInboundType]
	input := membership.Credential{Protocol: membership.VLESS, UUID: "4310b7cc-e6d2-4101-ac03-8ec2a553b835"}
	b.Run("prepare-vless", func(b *testing.B) {
		entry := &inboundAccounts{layout: layout}
		b.ReportAllocs()
		for b.Loop() {
			if _, err := entry.prepare("extra@example.invalid", input); err != nil {
				b.Fatal(err)
			}
		}
	})

	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprintf("existing=%d", count), func(b *testing.B) {
			const tag = "vless"
			conn := new(grpc.ClientConn) // Only identity is used by the warm edit.
			client := New(candidateConnection{conn})
			entry := &inboundAccounts{layout: layout, records: make(map[string]*User, count)}
			inbound := acceptedInbound{layout: layout, accounts: make(map[string]*User, count), full: true}
			e := &editor{entry: entry}
			for i := range count {
				name := fmt.Sprintf("profile-%05d@example.invalid", i)
				account, err := entry.prepare(name, input)
				if err != nil {
					b.Fatal(err)
				}
				user := e.user(account)
				entry.records[name] = user
				inbound.set(name, user)
			}
			client.cached.Store(&inventory{conn: conn, inbounds: map[string]*inboundAccounts{tag: entry}, size: count})
			client.accepted.Store(&acceptedMembership{inbounds: map[string]acceptedInbound{tag: inbound}})

			newAccount, err := entry.prepare("extra@example.invalid", input)
			if err != nil {
				b.Fatal(err)
			}
			present := "profile-00000@example.invalid"
			for _, workload := range []struct {
				name      string
				member    membership.PlannedMember
				removals  []string
				additions []membership.PreparedAccount
			}{
				{name: "add", member: membership.PlannedMember{Name: newAccount.Name, Replacement: &newAccount}, additions: []membership.PreparedAccount{newAccount}},
				{name: "remove", member: membership.PlannedMember{Name: present}, removals: []string{present}},
				{name: "no-change", member: membership.PlannedMember{Name: present, Keep: true}},
			} {
				b.Run("stage-"+workload.name, func(b *testing.B) {
					discard := errors.New("discard staged benchmark edit")
					members := []membership.PlannedMember{workload.member}
					stage := func(session membership.EditSession) error {
						if err := session.Inbounds()[0].Stage(false, members, workload.removals, workload.additions); err != nil {
							return err
						}
						if err := session.CheckStaged(); err != nil {
							return err
						}
						return discard
					}
					tags := []string{tag}
					b.ReportAllocs()
					for b.Loop() {
						if err := client.Edit(b.Context(), "primary", tags, stage); err != discard {
							b.Fatalf("stage = %v", err)
						}
					}
				})
			}
		})
	}
}
