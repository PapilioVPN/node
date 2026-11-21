package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type builderFixture struct {
	err   error
	calls int
}

func (b *builderFixture) Validate(context.Context, domain.Candidate) (domain.Validation, error) {
	b.calls++
	return domain.Validation{Failure: errors.New("private native credential")}, b.err
}
func TestTypedInputRejectionAndErrorMapping(t *testing.T) {
	b := &builderFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Builder: b}, {ID: "disabled"}})
	h := Handler{Service: service, Timeout: time.Second}
	valid := &configsv1.ValidateConfigurationRequest{InstanceId: "a", Format: "json", Content: []byte(`invalid`)}
	result, err := h.ValidateConfiguration(context.Background(), valid)
	if err != nil || result.Valid || result.InstanceId != "a" || result.CheckedAt.CheckValid() != nil || result.DurationNs < 0 {
		t.Fatalf("result=%v err=%v", result, err)
	}
	for _, test := range []struct {
		request *configsv1.ValidateConfigurationRequest
		code    codes.Code
	}{
		{nil, codes.InvalidArgument}, {&configsv1.ValidateConfigurationRequest{}, codes.InvalidArgument},
		{&configsv1.ValidateConfigurationRequest{InstanceId: "missing", Format: "json", Content: []byte(`{}`)}, codes.NotFound},
		{&configsv1.ValidateConfigurationRequest{InstanceId: "disabled", Format: "json", Content: []byte(`{}`)}, codes.FailedPrecondition},
		{&configsv1.ValidateConfigurationRequest{InstanceId: "a", Format: "json", Content: make([]byte, domain.MaxCandidateBytes+1)}, codes.ResourceExhausted},
	} {
		if _, err := h.ValidateConfiguration(context.Background(), test.request); status.Code(err) != test.code {
			t.Fatalf("wanted %v got %v", test.code, err)
		}
	}
	if b.calls != 1 {
		t.Fatal("rejected input reached native builder")
	}
	for _, test := range []struct {
		err  error
		code codes.Code
	}{{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {domain.ErrCapacity, codes.ResourceExhausted}, {errors.New("private source path"), codes.Unavailable}} {
		b.err = test.err
		_, err := h.ValidateConfiguration(context.Background(), valid)
		if status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("mapping %v", err)
		}
	}
}
