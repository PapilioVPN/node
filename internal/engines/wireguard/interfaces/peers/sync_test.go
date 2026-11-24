package peers

import (
	"crypto/sha256"
	"errors"
	"testing"
)

func TestPrepareRejectsAmbiguousPeerSets(t *testing.T) {
	first := sha256.Sum256([]byte("first peer"))
	second := sha256.Sum256([]byte("second peer"))
	for _, tc := range []struct {
		name  string
		input []Input
		want  error
	}{
		{"no set", nil, ErrInvalid},
		{"oversized set", make([]Input, MaxPeers+1), ErrTooMany},
		{"duplicate key", []Input{{PublicKey: first[:]}, {PublicKey: first[:]}}, ErrInvalid},
		{"duplicate normalized prefix", []Input{
			{PublicKey: first[:], AllowedIPs: []string{"192.0.2.1/24"}},
			{PublicKey: second[:], AllowedIPs: []string{"192.0.2.2/24"}},
		}, ErrInvalid},
		{"incorrect PSK size", []Input{{PublicKey: first[:], PresharedKey: []byte{1}}}, ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Prepare(tc.input); !errors.Is(err, tc.want) {
				t.Fatalf("Prepare returned %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDiffPreservesUnchangedPeerFields(t *testing.T) {
	key := sha256.Sum256([]byte("peer"))
	psk := sha256.Sum256([]byte("fixture PSK"))
	current, err := Prepare([]Input{{PublicKey: key[:], PresharedKey: psk[:], AllowedIPs: []string{"192.0.2.1/32", "fd00::1/128"}}})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := Prepare([]Input{{PublicKey: key[:], AllowedIPs: []string{"fd00::1/128", "192.0.2.1/32"}}})
	if err != nil {
		t.Fatal(err)
	}
	updates, removals, result := Diff(current, desired)
	if result != (Result{Updated: 1}) || len(removals) != 0 || len(updates) != 1 {
		t.Fatalf("unexpected changes: %+v", result)
	}
	if !updates[0].SetPresharedKey || updates[0].PresharedKey != [32]byte{} || updates[0].ReplaceAllowedIPs {
		t.Fatal("clearing the PSK also replaced unrelated fields")
	}
}
