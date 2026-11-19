package grpcapi_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/app/services"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/domain"
	"github.com/PapilioVPN/node/internal/apps/system/host/sessions/grpcapi"
	sessionsv1 "github.com/PapilioVPN/node/internal/apps/system/host/sessions/grpcapi/proto/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSessionGRPCPresencePrecisionAndErrors(t *testing.T) {
	logind, records := &logindFixture{}, &recordsFixture{}
	handler := grpcapi.Handler{Timeout: time.Second, Service: services.Service{Logind: logind, Records: records}}
	page, err := handler.ListSessions(context.Background(), &sessionsv1.ListSessionsRequest{Offset: 1, Limit: 2})
	if err != nil || len(page.Sessions) != 1 || page.Sessions[0].Uid != 0 || page.Sessions[0].LeaderPid == nil || *page.Sessions[0].LeaderPid != 0 || page.Sessions[0].Idle == nil || *page.Sessions[0].Idle || page.NextOffset == nil || *page.NextOffset != 1 || logind.selection.Offset != 1 || logind.selection.Limit != 2 {
		t.Fatalf("%v %v", page, err)
	}
	logind.basic = true
	page, err = handler.ListSessions(context.Background(), &sessionsv1.ListSessionsRequest{})
	if err != nil || page.Extended || page.Sessions[0].LeaderPid != nil || page.Sessions[0].Idle != nil {
		t.Fatalf("invented basic metadata: %v %v", page, err)
	}
	read, err := handler.GetSession(context.Background(), &sessionsv1.GetSessionRequest{Id: "s1"})
	if err != nil || read.Session.StartedUnixUs == nil || *read.Session.StartedUnixUs != 9007199254740993 || read.Session.Remote == nil || *read.Session.Remote || read.Session.RemoteHost != nil || logind.id != "s1" {
		t.Fatalf("%v %v", read, err)
	}
	legacy, err := handler.ListLoginRecords(context.Background(), &sessionsv1.ListLoginRecordsRequest{Offset: 1, Limit: 2})
	if err != nil || len(legacy.Records) != 2 || legacy.Records[0].StartedUnixUs != -500000 || legacy.Records[1].RecordId != "p1" || records.selection.Offset != 1 {
		t.Fatalf("%v %v", legacy, err)
	}
	before := logind.calls + records.calls
	if _, err := handler.GetSession(context.Background(), &sessionsv1.GetSessionRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if _, err := handler.ListLoginRecords(context.Background(), &sessionsv1.ListLoginRecordsRequest{Limit: 257}); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if logind.calls+records.calls != before {
		t.Fatal("invalid input reached native readers")
	}
	for _, failure := range []struct {
		cause error
		code  codes.Code
	}{{domain.ErrNotFound, codes.NotFound}, {domain.ErrPermissionDenied, codes.PermissionDenied}, {domain.ErrUnsupported, codes.Unimplemented}, {domain.ErrResourceExhausted, codes.ResourceExhausted}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded}, {errors.New("private source"), codes.Unavailable}} {
		logind.cause = failure.cause
		if result, err := handler.GetSession(context.Background(), &sessionsv1.GetSessionRequest{Id: "s1"}); result != nil || status.Code(err) != failure.code || strings.Contains(err.Error(), "private") {
			t.Fatalf("%v %v", result, err)
		}
	}
}
