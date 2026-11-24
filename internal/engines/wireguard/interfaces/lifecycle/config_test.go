package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/engines/wireguard/interfaces/status"
)

const validSource = `{"private_key":"AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=","addresses":["192.0.2.1/24"]}`

func TestConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		issue  ConfigIssue
	}{
		{"valid", validSource, ConfigIssueNone},
		{"empty", "", ConfigIssueInvalidJSON},
		{"null", "null", ConfigIssueInvalidJSON},
		{"non-JSON whitespace", validSource + "\u00a0", ConfigIssueInvalidJSON},
		{"trailing JSON", validSource + "{}", ConfigIssueInvalidJSON},
		{"unknown field", strings.TrimSuffix(validSource, "}") + `,"secret":"private-value"}`, ConfigIssueInvalidJSON},
		{"invalid key encoding", `{"private_key":"invalid-key!"}`, ConfigIssueInvalidPrivateKey},
		{"zero key", `{"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`, ConfigIssueInvalidPrivateKey},
		{"missing addresses", strings.Replace(validSource, `["192.0.2.1/24"]`, "[]", 1), ConfigIssueInvalidAddresses},
		{"invalid prefix", strings.Replace(validSource, "/24", "/0", 1), ConfigIssueInvalidAddresses},
		{"duplicate address", strings.Replace(validSource, `"192.0.2.1/24"`, `"192.0.2.1/24","192.0.2.1/32"`, 1), ConfigIssueDuplicateAddress},
		{"MTU", strings.TrimSuffix(validSource, "}") + `,"mtu":-1}`, ConfigIssueInvalidMTU},
		{"IPv6 MTU", strings.Replace(strings.TrimSuffix(validSource, "}"), "192.0.2.1/24", "fd00::1/64", 1) + `,"mtu":1279}`, ConfigIssueInvalidMTU},
		{"port", strings.TrimSuffix(validSource, "}") + `,"listen_port":65536}`, ConfigIssueInvalidListenPort},
		{"maximum size", validSource + strings.Repeat(" ", MaxConfigBytes-len(validSource)), ConfigIssueNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validator := NewValidator("wg0")
			issue, err := validator.Check(t.Context(), "wg0", []byte(tc.source))
			if err != nil || issue != tc.issue {
				t.Fatalf("Check returned %d, %v, want %d", issue, err, tc.issue)
			}
			_, err = parseConfig([]byte(tc.source))
			if tc.issue != ConfigIssueNone && (!errors.Is(err, ErrInvalid) || !errors.Is(err, tc.issue)) {
				t.Fatalf("Start parser returned %v, want %v", err, tc.issue)
			}
		})
	}
}

func TestConfigValidationAdmission(t *testing.T) {
	validator := NewValidator("wg0")
	ctx, cancel := context.WithDeadline(t.Context(), time.Time{})
	defer cancel()
	if _, err := validator.Check(ctx, "wg0", []byte(validSource)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired validation = %v", err)
	}
	validator.mu.Lock()
	defer validator.mu.Unlock()
	if _, err := validator.Check(t.Context(), "wg0", []byte(validSource)); !errors.Is(err, status.ErrBusy) {
		t.Fatalf("overlapping validation = %v", err)
	}
}

func FuzzConfigValidation(f *testing.F) {
	f.Add([]byte(validSource))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > MaxConfigBytes {
			t.Skip()
		}
		validator := NewValidator("wg0")
		issue, err := validator.Check(t.Context(), "wg0", source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = parseConfig(source)
		if issue == ConfigIssueNone && err != nil || issue != ConfigIssueNone && !errors.Is(err, issue) {
			t.Fatalf("validator and Start parser differed, issue %v, parse error %v", issue, err)
		}
	})
}
