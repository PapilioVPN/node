package membership

import (
	"encoding/binary"
	"fmt"
	"testing"
)

// The credential is already prepared here so this measures planning and index
// lookups separately from native credential encoding and membership staging.
type planningEditor struct {
	Editor
	names   map[string]Key
	ids     map[IDKey]bool
	account PreparedAccount
}

func (e planningEditor) Name(email string) (string, error) { return NameKey(email), nil }
func (e planningEditor) Lookup(name string) (Key, bool) {
	key, ok := e.names[name]
	return key, ok
}
func (e planningEditor) HasID(id IDKey) bool { return e.ids[id] }
func (e planningEditor) Prepare(string, Credential) (PreparedAccount, error) {
	return e.account, nil
}

func BenchmarkSyncUserPlanning(b *testing.B) {
	const existing = "profile-00000@example.invalid"
	const extra = "extra@example.invalid"
	credential := &Credential{Protocol: VLESS}
	for _, count := range []int{1, 1000, 10000} {
		b.Run(fmt.Sprintf("existing=%d", count), func(b *testing.B) {
			e := &planningEditor{names: make(map[string]Key, count), ids: make(map[IDKey]bool, count)}
			for i := range count {
				name := fmt.Sprintf("profile-%05d@example.invalid", i)
				var key Key
				binary.LittleEndian.PutUint64(key.Identity[:], uint64(i+1))
				e.names[name] = key
				e.ids[key.Identity] = true
			}

			for _, workload := range []struct {
				name           string
				change         change
				key            Key
				added, removed int
			}{
				{name: "add", change: change{email: extra, credential: credential}, added: 1},
				{name: "remove", change: change{email: existing}, removed: 1},
				{name: "no-change", change: change{email: existing, credential: credential}, key: e.names[existing]},
			} {
				b.Run(workload.name, func(b *testing.B) {
					e.account = PreparedAccount{Name: workload.change.email, Email: workload.change.email, Key: workload.key}
					changes := []change{workload.change}
					plan, err := preparePlan(e, changes, false)
					if err != nil || len(plan.additions) != workload.added || len(plan.removals) != workload.removed {
						b.Fatalf("preparation = %+v, %v", plan, err)
					}
					b.ReportAllocs()
					for b.Loop() {
						if _, err := preparePlan(e, changes, false); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
