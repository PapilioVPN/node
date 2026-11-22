package services

import (
	"strings"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/observability/logs/domain"
)

func FuzzResumePosition(f *testing.F) {
	f.Add(encode(position{instance: "edge", execution: "run-a", generation: 1, offset: 123, skipping: true}))
	f.Add("bad")
	f.Fuzz(func(t *testing.T, input string) {
		p, err := decode(input, "edge")
		if err != nil {
			return
		}
		if p.offset > domain.MaxOutputBytes || p.instance != "edge" || strings.ContainsRune(p.execution, 0) {
			t.Fatal(p)
		}
		next, err := decode(encode(p), "edge")
		if err != nil || next != p {
			t.Fatal(next, err)
		}
	})
}

func TestInvalidResumeExecutionSelector(t *testing.T) {
	for _, execution := range []string{"private\nvalue", string([]byte{255}), "run/other", ""} {
		if _, err := decode(encode(position{instance: "edge", execution: execution, generation: 1}), "edge"); err == nil {
			t.Fatal("invalid execution selector accepted")
		}
	}
}
