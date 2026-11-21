package services

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type compilerFixture struct {
	calls int
	count int
	err   error
}

func (c *compilerFixture) Compile(_ context.Context, sources []domain.Source) (domain.Compilation, error) {
	c.calls++
	c.count = len(sources)
	return domain.Compilation{Content: []byte{1, 2, 3}, SHA256: "native-content-digest"}, c.err
}

func TestOrderedSourceSetUsesOneNativeCompilation(t *testing.T) {
	for _, count := range []int{1, 8, 32} {
		compiler := &compilerFixture{}
		service, _ := New([]Binding{{ID: "a", Compiler: compiler}})
		sources := make([]domain.Source, count)
		for i := range sources {
			sources[i] = domain.Source{Name: fmt.Sprintf("%02d.json", i), Content: []byte(`{}`)}
		}
		result, err := service.Compile(context.Background(), "a", sources)
		if err != nil || compiler.calls != 1 || compiler.count != count || result.InstanceID != "a" || result.PreparedAt.IsZero() || result.Duration < 0 || result.SHA256 != "native-content-digest" || len(result.Content) != 3 {
			t.Fatalf("%d sources: %+v %v native=%+v", count, result, err, compiler)
		}
	}
}

func TestCompilationPreflightRejectsUnrepresentableSourcesWithoutNativeWork(t *testing.T) {
	compiler := &compilerFixture{}
	service, _ := New([]Binding{{ID: "a", Compiler: compiler}, {ID: "disabled"}})
	valid := []domain.Source{{Name: "base.json", Content: []byte(`{}`)}}
	for _, test := range []struct {
		id      string
		sources []domain.Source
		want    error
	}{
		{"", valid, domain.ErrInvalidArgument}, {"missing", valid, domain.ErrNotFound}, {"disabled", valid, domain.ErrUnsupported},
		{"a", nil, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "../outside.json", Content: []byte(`{}`)}}, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "bad\\file.json", Content: []byte(`{}`)}}, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "base.json", Content: []byte(`{}`)}, {Name: "base.json", Content: []byte(`{}`)}}, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "base.pb", Content: []byte{1}}, {Name: "extra.json", Content: []byte(`{}`)}}, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "base.lua", Content: []byte(`{}`)}}, domain.ErrInvalidArgument},
		{"a", []domain.Source{{Name: "base.json"}}, domain.ErrInvalidArgument},
		{"a", make([]domain.Source, 33), domain.ErrTooLarge},
		{"a", []domain.Source{{Name: "base.json", Content: make([]byte, domain.MaxCandidateBytes)}, {Name: "extra.json", Content: []byte(`{}`)}}, domain.ErrTooLarge},
	} {
		if _, err := service.Compile(context.Background(), test.id, test.sources); !errors.Is(err, test.want) {
			t.Fatalf("%s: wanted %v got %v", test.id, test.want, err)
		}
	}
	if compiler.calls != 0 {
		t.Fatal("preflight dispatched native work")
	}
	if _, err := service.Compile(context.Background(), "a", []domain.Source{{Name: "empty.pb"}}); err != nil {
		t.Fatal("native empty protobuf rejected before dispatch")
	}
	compiler.err = domain.ErrRejected
	if result, err := service.Compile(context.Background(), "a", valid); !errors.Is(err, domain.ErrRejected) || result.Content != nil {
		t.Fatal("failed compilation returned successful partial bytes")
	}
}
