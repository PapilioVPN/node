package services

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
)

type builderFixture struct {
	calls   int
	result  domain.Validation
	err     error
	content []byte
}

func (b *builderFixture) Validate(ctx context.Context, c domain.Candidate) (domain.Validation, error) {
	b.calls++
	b.content = c.Content
	return b.result, b.err
}

func TestSelectionValidationAndSingleNativeCall(t *testing.T) {
	b := &builderFixture{result: domain.Validation{Valid: true}}
	s, err := New([]Binding{{"a", b}, {"disabled", nil}})
	if err != nil {
		t.Fatal(err)
	}
	input := domain.Candidate{Format: domain.JSON, Content: []byte(`{}`)}
	result, err := s.Validate(context.Background(), "a", input)
	if err != nil || !result.Valid || result.InstanceID != "a" || result.CheckedAt.IsZero() || result.Duration < 0 || b.calls != 1 || &b.content[0] != &input.Content[0] {
		t.Fatalf("result=%+v err=%v calls=%d", result, err, b.calls)
	}
	for _, test := range []struct {
		id    string
		input domain.Candidate
		want  error
	}{
		{"missing", input, domain.ErrNotFound}, {"", input, domain.ErrInvalidArgument}, {"disabled", input, domain.ErrUnsupported},
		{"a", domain.Candidate{Format: "../escape", Content: []byte(`{}`)}, domain.ErrInvalidArgument},
		{"a", domain.Candidate{Format: domain.JSON}, domain.ErrInvalidArgument},
		{"a", domain.Candidate{Format: domain.JSON, Content: make([]byte, domain.MaxCandidateBytes+1)}, domain.ErrTooLarge},
	} {
		if _, err := s.Validate(context.Background(), test.id, test.input); !errors.Is(err, test.want) {
			t.Fatalf("%q: %v", test.id, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Validate(ctx, "a", input); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if b.calls != 1 {
		t.Fatal("rejected selection reached native builder")
	}
}

func TestRejectionAndNativeFailureRemainDistinct(t *testing.T) {
	cause := errors.New("private native failure")
	b := &builderFixture{result: domain.Validation{Failure: cause}}
	s, _ := New([]Binding{{"a", b}})
	input := domain.Candidate{Format: domain.JSON, Content: []byte(`bad`)}
	result, err := s.Validate(context.Background(), "a", input)
	if err != nil || result.Valid || !errors.Is(result.Failure, cause) || result.CheckedAt.IsZero() {
		t.Fatalf("%+v %v", result, err)
	}
	b.err = cause
	result, err = s.Validate(context.Background(), "a", input)
	if !errors.Is(err, cause) || result.InstanceID != "" {
		t.Fatal("native operation error became a validation result")
	}
	if _, err := New([]Binding{{"a", nil}, {"a", nil}}); !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatal("duplicate disabled binding accepted")
	}
}
