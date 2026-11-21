//go:build integration && linux

package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

func TestSourceValidationUsesOneConverterForWholeCollection(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	admission, _ := NewAdmission(1)
	builder, err := New(Config{Binary: binary, Directory: work, WorkDirectory: work, MaxOutputBytes: 1}, admission)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, 8, 32} {
		marker := filepath.Join(t.TempDir(), "invocations")
		sources := make([]domain.Source, count)
		for i := range sources {
			sources[i] = domain.Source{Name: fmt.Sprintf("%02d.json", i), Content: []byte(`{}`)}
		}
		sources[0].Content = []byte("fixture-invocation:" + marker)
		result, err := builder.ValidateSources(context.Background(), sources)
		if err != nil || !result.Valid {
			t.Fatalf("count %d: %+v %v", count, result, err)
		}
		record, err := os.ReadFile(marker)
		if err != nil || string(record) != strconv.Itoa(count)+"\n" {
			t.Fatalf("count %d: converter invocations=%q %v", count, record, err)
		}
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 0 {
		t.Fatalf("validation staging leaked: %v %v", entries, err)
	}
}
