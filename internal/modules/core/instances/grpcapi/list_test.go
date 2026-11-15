package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/core/instances/app/services"
	"github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi"
	instancesv1 "github.com/PapilioVPN/node/internal/modules/core/instances/grpcapi/proto/v1"
)

func TestListAuthorizationPrecedesPageSelection(t *testing.T) {
	for _, test := range []struct {
		ctx           context.Context
		read, control bool
		code          codes.Code
	}{
		{context.Background(), true, true, codes.Unauthenticated},
		{verifiedContext(t), false, false, codes.PermissionDenied},
		{verifiedContext(t), false, true, codes.PermissionDenied},
	} {
		// A nil collection reader detects any lookup before authentication/grants.
		handler := grpcapi.Handler{AllowStatusRead: test.read, AllowControl: test.control}
		if _, err := handler.ListInstanceStatuses(test.ctx, &instancesv1.ListInstanceStatusesRequest{}); status.Code(err) != test.code {
			t.Fatalf("list authorization = %v; want %v", err, test.code)
		}
	}
}

type statusesReader func(context.Context, domain.StatusPageInput) (domain.StatusPage, error)

func (f statusesReader) ListInstanceStatuses(ctx context.Context, input domain.StatusPageInput) (domain.StatusPage, error) {
	return f(ctx, input)
}

func TestListPreservesSelectionAndReadableExecutionFailure(t *testing.T) {
	ctx := verifiedContext(t)
	handler := grpcapi.Handler{AllowStatusRead: true, Statuses: statusesReader(func(actual context.Context, input domain.StatusPageInput) (domain.StatusPage, error) {
		if actual != ctx || input.AfterID != " edge-a " || input.PageSize != 2 {
			t.Fatal("selection/context changed at the transport boundary")
		}
		return domain.StatusPage{Items: []domain.Status{
			{Instance: domain.Instance{ID: "edge-b", Kind: domain.Xray}, State: domain.Running},
			{Instance: domain.Instance{ID: "edge-c", Kind: domain.Xray}, State: domain.Exited, Failure: controlExitCode(23)},
		}, NextAfterID: "edge-c"}, nil
	})}
	response, err := handler.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: 2, AfterInstanceId: " edge-a "})
	if err != nil || len(response.GetItems()) != 2 || response.NextAfterInstanceId != "edge-c" {
		t.Fatalf("collection response: %v, %v", response, err)
	}
	if response.Items[0].InstanceId != "edge-b" || response.Items[0].Engine != "xray" || response.Items[0].State != instancesv1.InstanceState_INSTANCE_STATE_RUNNING || response.Items[0].Failure != nil {
		t.Fatalf("first observation changed: %v", response)
	}
	failed := response.Items[1]
	if failed.InstanceId != "edge-c" || failed.State != instancesv1.InstanceState_INSTANCE_STATE_EXITED || failed.Failure == nil || failed.Failure.ExitCode == nil || *failed.Failure.ExitCode != 23 || strings.Contains(response.String(), "private") {
		t.Fatalf("readable execution failure changed or leaked: %v", response)
	}
}

func TestListInvalidSizeAndAbsentRequest(t *testing.T) {
	handler := grpcapi.Handler{AllowStatusRead: true, Statuses: services.NewLifecycleService(nil)}
	for _, request := range []*instancesv1.ListInstanceStatusesRequest{nil, {}, {PageSize: -1}} {
		if response, err := handler.ListInstanceStatuses(verifiedContext(t), request); response != nil || status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid page: %v, %v", response, err)
		}
	}
	empty, err := handler.ListInstanceStatuses(verifiedContext(t), &instancesv1.ListInstanceStatusesRequest{PageSize: 1})
	if err != nil || len(empty.GetItems()) != 0 || empty.NextAfterInstanceId != "" {
		t.Fatalf("empty configuration: %v, %v", empty, err)
	}
}

func TestListErrorsNeverExposeSuccessfulPartialPages(t *testing.T) {
	for cause, expected := range map[error]codes.Code{
		fmt.Errorf("private: %w", context.Canceled):         codes.Canceled,
		fmt.Errorf("private: %w", context.DeadlineExceeded): codes.DeadlineExceeded,
		fmt.Errorf("private: %w", domain.ErrInvalidInput):   codes.InvalidArgument,
		errors.New("private native path"):                   codes.Internal,
	} {
		handler := grpcapi.Handler{AllowStatusRead: true, Statuses: statusesReader(func(context.Context, domain.StatusPageInput) (domain.StatusPage, error) {
			return domain.StatusPage{Items: []domain.Status{{Instance: domain.Instance{ID: "partial"}, State: domain.Running}}, NextAfterID: "partial"}, cause
		})}
		response, err := handler.ListInstanceStatuses(verifiedContext(t), &instancesv1.ListInstanceStatusesRequest{PageSize: 1})
		if response != nil || status.Code(err) != expected || strings.Contains(err.Error(), "private") {
			t.Fatalf("partial/unsanitized result: %v, %v", response, err)
		}
	}
}

func TestListCancellationAfterReadDoesNotEncodeSuccess(t *testing.T) {
	for _, items := range [][]domain.Status{nil, {{State: domain.Running}}} {
		ctx, cancel := context.WithCancel(verifiedContext(t))
		handler := grpcapi.Handler{AllowStatusRead: true, Statuses: statusesReader(func(context.Context, domain.StatusPageInput) (domain.StatusPage, error) {
			cancel()
			return domain.StatusPage{Items: items}, nil
		})}
		response, err := handler.ListInstanceStatuses(ctx, &instancesv1.ListInstanceStatusesRequest{PageSize: 1})
		cancel()
		if response != nil || status.Code(err) != codes.Canceled {
			t.Fatalf("cancellation after read became success: %v, %v", response, err)
		}
	}
}
