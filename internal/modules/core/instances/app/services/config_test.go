package services_test

import (
	"context"
	"errors"
	"testing"

	"github.com/PapilioVPN/node/internal/modules/core/instances/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
)

type configValidator func(context.Context, domain.ConfigCandidate) (domain.ConfigValidation, error)

func (f configValidator) ValidateConfig(ctx context.Context, candidate domain.ConfigCandidate) (domain.ConfigValidation, error) {
	return f(ctx, candidate)
}

func TestConfigValidationSelectsOneOwnedAdapter(t *testing.T) {
	ctx := t.Context()
	content := []byte("native bytes")
	called := 0
	bindings := map[domain.InstanceID]services.ConfigValidator{
		"edge-a": configValidator(func(received context.Context, input domain.ConfigCandidate) (domain.ConfigValidation, error) {
			called++
			if received != ctx || input.Format != domain.TOML || &input.Content[0] != &content[0] {
				t.Fatal("context/format/borrowed native bytes changed")
			}
			return domain.ConfigValidation{Valid: true}, nil
		}),
		"edge-b": configValidator(func(context.Context, domain.ConfigCandidate) (domain.ConfigValidation, error) {
			t.Fatal("unselected instance was called")
			return domain.ConfigValidation{}, nil
		}),
		"disabled": nil,
	}
	service := services.NewConfigService(bindings)
	delete(bindings, "edge-a") // The service owns its immutable identity index.
	result, err := service.ValidateInstanceConfig(ctx, "edge-a", domain.ConfigCandidate{Format: domain.TOML, Content: content})
	if err != nil || !result.Valid || called != 1 {
		t.Fatalf("selected validation = %+v, %v, calls=%d", result, err, called)
	}
	for id, want := range map[domain.InstanceID]error{"missing": domain.ErrNotFound, "disabled": domain.ErrValidationDisabled, " edge-a ": domain.ErrNotFound} {
		if _, err := service.ValidateInstanceConfig(ctx, id, domain.ConfigCandidate{}); !errors.Is(err, want) {
			t.Fatalf("%q: %v; want %v", id, err, want)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.ValidateInstanceConfig(canceled, "missing", domain.ConfigCandidate{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled selection: %v", err)
	}
}

func TestConfigValidationPreservesRejectionAndExecutionErrors(t *testing.T) {
	rejection := errors.New("controlled native rejection")
	failure := errors.New("controlled execution error")
	for _, test := range []struct {
		result domain.ConfigValidation
		err    error
	}{
		{result: domain.ConfigValidation{Failure: rejection}},
		{result: domain.ConfigValidation{Valid: true}, err: failure},
	} {
		service := services.NewConfigService(map[domain.InstanceID]services.ConfigValidator{"edge": configValidator(func(context.Context, domain.ConfigCandidate) (domain.ConfigValidation, error) {
			return test.result, test.err
		})})
		result, err := service.ValidateInstanceConfig(t.Context(), "edge", domain.ConfigCandidate{})
		if test.err != nil {
			if !errors.Is(err, failure) || result.Valid || result.Failure != nil {
				t.Fatalf("execution failure became a successful result: %+v, %v", result, err)
			}
		} else if err != nil || result.Valid || !errors.Is(result.Failure, rejection) {
			t.Fatalf("native rejection was lost: %+v, %v", result, err)
		}
	}
}

func TestConfigValidationCancellationDuringAdapterCall(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	service := services.NewConfigService(map[domain.InstanceID]services.ConfigValidator{"edge": configValidator(func(context.Context, domain.ConfigCandidate) (domain.ConfigValidation, error) {
		cancel()
		return domain.ConfigValidation{Valid: true}, nil
	})})
	result, err := service.ValidateInstanceConfig(ctx, "edge", domain.ConfigCandidate{})
	if !errors.Is(err, context.Canceled) || result.Valid {
		t.Fatalf("canceled validation became success: %+v, %v", result, err)
	}
}
