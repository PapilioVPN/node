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

type compilerFixture struct {
	calls int
	err   error
}

func (c *compilerFixture) Compile(context.Context, []domain.Source) (domain.Compilation, error) {
	c.calls++
	return domain.Compilation{Content: []byte{1, 2, 3}, SHA256: "digest"}, c.err
}

func TestGRPCCompilationShapeAndFailureMapping(t *testing.T) {
	compiler := &compilerFixture{}
	service, _ := services.New([]services.Binding{{ID: "a", Compiler: compiler}})
	h := Handler{Service: service, Timeout: time.Second}
	valid := &configsv1.CompileConfigurationRequest{InstanceId: "a", Sources: []*configsv1.ConfigurationSource{{Name: "base.json", Content: []byte(`{}`)}}}
	result, err := h.CompileConfiguration(context.Background(), valid)
	if err != nil || result.InstanceId != "a" || len(result.Content) != 3 || result.Sha256 != "digest" || result.PreparedAt.CheckValid() != nil {
		t.Fatalf("result=%v error=%v", result, err)
	}
	for _, test := range []struct {
		input *configsv1.CompileConfigurationRequest
		code  codes.Code
	}{
		{nil, codes.InvalidArgument}, {&configsv1.CompileConfigurationRequest{InstanceId: "a", Sources: []*configsv1.ConfigurationSource{nil}}, codes.InvalidArgument},
		{&configsv1.CompileConfigurationRequest{InstanceId: "a", Sources: make([]*configsv1.ConfigurationSource, 33)}, codes.ResourceExhausted},
	} {
		if _, err := h.CompileConfiguration(context.Background(), test.input); status.Code(err) != test.code {
			t.Fatalf("wrong input status: %v", err)
		}
	}
	if compiler.calls != 1 {
		t.Fatal("invalid gRPC input reached native compiler")
	}
	for _, test := range []struct {
		err  error
		code codes.Code
	}{{domain.ErrRejected, codes.InvalidArgument}, {domain.ErrTooLarge, codes.ResourceExhausted}, {context.Canceled, codes.Canceled}, {errors.New("private compiled source"), codes.Unavailable}} {
		compiler.err = test.err
		result, err := h.CompileConfiguration(context.Background(), valid)
		if result != nil || status.Code(err) != test.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("wrong native error mapping: %v", err)
		}
	}
}
