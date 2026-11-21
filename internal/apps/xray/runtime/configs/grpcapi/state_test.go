package grpcapi

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	configsv1 "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/grpcapi/proto/v1"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStateGRPCRequiresExplicitRevision(t *testing.T) {
	h := Handler{Timeout: time.Second}
	for _, input := range []*configsv1.ReplaceConfigurationRequest{nil, {InstanceId: "a"}} {
		if _, err := h.ReplaceConfiguration(context.Background(), input); status.Code(err) != codes.InvalidArgument {
			t.Fatal(err)
		}
	}
	if _, err := h.ApplyConfiguration(context.Background(), &configsv1.ApplyConfigurationRequest{InstanceId: "a"}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	revision := uint64(0)
	if _, err := h.ReplaceConfiguration(context.Background(), &configsv1.ReplaceConfigurationRequest{InstanceId: "a", ExpectedRevision: &revision, Sources: []*configsv1.ConfigurationSource{nil}}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
}
func TestStateGRPCErrorDetailsPreserveIndependentEffects(t *testing.T) {
	revision := uint64(math.MaxUint64)
	result := domain.Change{Persistence: domain.Persistence{Published: true, Durable: true, Metadata: domain.DesiredMetadata{Revision: revision, Durable: true}}, Execution: &instances.Control{Effect: instances.Partial, Status: &instances.Status{ID: "a", Execution: instances.Execution{ConfigurationRevision: &revision, CleanupFailure: errors.New("private secret path")}}}}
	response, err := changeResult("a", result, errors.New("private native credential"))
	if response != nil || status.Code(err) != codes.Unavailable || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	details := status.Convert(err).Details()
	if len(details) != 1 {
		t.Fatal("failure discarded change evidence")
	}
	change, ok := details[0].(*configsv1.ConfigurationChange)
	if !ok || !change.Persistence.Durable || !change.Persistence.Published || change.Persistence.Metadata.Revision != revision || change.Execution.Effect != "partial" || *change.Execution.Status.ConfigurationRevision != revision || !change.Execution.Status.ConfigurationCleanupFailed {
		t.Fatalf("lost effect detail %+v", details)
	}
	if status.Code(failure(domain.ErrConflict)) != codes.Aborted {
		t.Fatal("revision conflict is not explicit")
	}
}
