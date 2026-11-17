package grpcapi_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	trafficgrpc "github.com/PapilioVPN/node/internal/apps/xray/network/traffic/grpcapi"
	infogrpc "github.com/PapilioVPN/node/internal/apps/xray/runtime/info/grpcapi"
	"github.com/PapilioVPN/node/internal/apps/xray/users/online/domain"
	online "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi"
	onlinev1 "github.com/PapilioVPN/node/internal/apps/xray/users/online/grpcapi/proto/v1"
	grpcapi "github.com/PapilioVPN/node/internal/bootstrap/xrayapi"
	"github.com/PapilioVPN/node/internal/testutil"
)

type onlineService struct {
	count func(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnline, error)
	ips   func(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnlineIPs, error)
	users func(context.Context, instances.InstanceID) (domain.InstanceOnlineUsers, error)
}

func (s onlineService) GetUserOnlineStats(ctx context.Context, id instances.InstanceID, email domain.UserEmail) (domain.InstanceUserOnline, error) {
	return s.count(ctx, id, email)
}
func (s onlineService) ListUserOnlineIPs(ctx context.Context, id instances.InstanceID, email domain.UserEmail) (domain.InstanceUserOnlineIPs, error) {
	return s.ips(ctx, id, email)
}
func (s onlineService) ListOnlineUsers(ctx context.Context, id instances.InstanceID) (domain.InstanceOnlineUsers, error) {
	return s.users(ctx, id)
}

func TestOnlineIdentityGrantAndShapePrecedeApplication(t *testing.T) {
	for _, test := range []struct {
		ctx   context.Context
		grant bool
		code  codes.Code
	}{
		{t.Context(), true, codes.Unauthenticated}, {testutil.VerifiedGRPCContext(t), false, codes.PermissionDenied}, {testutil.VerifiedGRPCContext(t), true, codes.InvalidArgument},
	} {
		h := grpcapi.Handler{TrafficAPI: trafficgrpc.TrafficAPI{AllowStatsRead: true}, InfoAPI: infogrpc.InfoAPI{AllowInfoRead: true}, OnlineAPI: online.OnlineAPI{AllowOnlineRead: test.grant}}
		_, count := h.GetUserOnlineStats(test.ctx, nil)
		_, ips := h.ListUserOnlineIPs(test.ctx, nil)
		_, users := h.ListOnlineUsers(test.ctx, nil)
		for _, err := range []error{count, ips, users} {
			if status.Code(err) != test.code {
				t.Fatalf("online guard before shape/selection: %v", err)
			}
		}
	}
	h := grpcapi.Handler{OnlineAPI: online.OnlineAPI{AllowOnlineRead: true}}
	ctx := testutil.VerifiedGRPCContext(t)
	for _, id := range []string{"", " ", "edge"} {
		_, count := h.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: id})
		_, ips := h.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: id})
		for _, err := range []error{count, ips} {
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("missing identity reached application: %v", err)
			}
		}
	}
}

func TestOnlineWirePreservesIdentityPrecisionOrderingAndEmptyCollections(t *testing.T) {
	ctx := testutil.VerifiedGRPCContext(t)
	observed := time.UnixMilli(1700000000123)
	const email domain.UserEmail = "   >>> Mixed@Example "
	for _, empty := range []bool{false, true} {
		calls := 0
		check := func(received context.Context, id instances.InstanceID) {
			calls++
			if received != ctx || id != " edge " {
				t.Fatal("instance/context changed")
			}
		}
		h := grpcapi.Handler{OnlineAPI: online.OnlineAPI{AllowOnlineRead: true, Online: onlineService{
			count: func(received context.Context, id instances.InstanceID, e domain.UserEmail) (domain.InstanceUserOnline, error) {
				check(received, id)
				if e != email {
					t.Fatal("email changed")
				}
				return domain.InstanceUserOnline{InstanceID: id, Email: e, Online: domain.UserOnline{OnlineIPCount: math.MaxInt64, ObservedAt: observed}}, nil
			},
			ips: func(received context.Context, id instances.InstanceID, e domain.UserEmail) (domain.InstanceUserOnlineIPs, error) {
				check(received, id)
				if e != email {
					t.Fatal("email changed")
				}
				result := domain.UserOnlineIPs{ObservedAt: observed}
				if !empty {
					result.Items = []domain.OnlineIP{{IP: "192.0.2.1", LastSeenUnixSeconds: math.MinInt64}, {IP: "2001:db8::1", LastSeenUnixSeconds: math.MaxInt64}}
				}
				return domain.InstanceUserOnlineIPs{InstanceID: id, Email: e, IPs: result}, nil
			},
			users: func(received context.Context, id instances.InstanceID) (domain.InstanceOnlineUsers, error) {
				check(received, id)
				result := domain.OnlineUsers{ObservedAt: observed}
				if !empty {
					result.Emails = []domain.UserEmail{email, email, "z"}
				}
				return domain.InstanceOnlineUsers{InstanceID: id, Users: result}, nil
			},
		}}}
		count, err := h.GetUserOnlineStats(ctx, &onlinev1.GetUserOnlineStatsRequest{InstanceId: " edge ", Email: string(email)})
		if err != nil || count.InstanceId != " edge " || count.Email != string(email) || count.OnlineIpCount != math.MaxInt64 || count.ObservedAtUnixMs != observed.UnixMilli() {
			t.Fatalf("count wire: %v, %v", count, err)
		}
		ips, err := h.ListUserOnlineIPs(ctx, &onlinev1.ListUserOnlineIPsRequest{InstanceId: " edge ", Email: string(email)})
		if err != nil || ips.InstanceId != " edge " || ips.Email != string(email) || ips.ObservedAtUnixMs != observed.UnixMilli() {
			t.Fatalf("IP wire: %v, %v", ips, err)
		}
		users, err := h.ListOnlineUsers(ctx, &onlinev1.ListOnlineUsersRequest{InstanceId: " edge "})
		if err != nil || users.InstanceId != " edge " || users.ObservedAtUnixMs != observed.UnixMilli() {
			t.Fatalf("users wire: %v, %v", users, err)
		}
		if empty {
			if len(ips.Items) != 0 || len(users.Emails) != 0 {
				t.Fatal("empty result synthesized data")
			}
		} else {
			if len(ips.Items) != 2 || ips.Items[0].Ip != "192.0.2.1" || ips.Items[0].LastSeenUnixSeconds != math.MinInt64 || ips.Items[1].Ip != "2001:db8::1" || ips.Items[1].LastSeenUnixSeconds != math.MaxInt64 || !reflect.DeepEqual(users.Emails, []string{string(email), string(email), "z"}) {
				t.Fatalf("collection changed: %v, %v", ips, users)
			}
		}
		if calls != 3 {
			t.Fatalf("application calls: %d", calls)
		}
	}
}

func TestOnlineErrorsDiscardPartialFactsAndSanitizeNativeCauses(t *testing.T) {
	for cause, code := range map[error]codes.Code{
		instances.ErrNotFound: codes.NotFound, domain.ErrOnlineObservationNotFound: codes.NotFound, domain.ErrAPIDisabled: codes.FailedPrecondition, domain.ErrOnlineUnsupported: codes.FailedPrecondition, domain.ErrAPIResourceLimit: codes.ResourceExhausted, domain.ErrAPIBusy: codes.ResourceExhausted, domain.ErrInvalidOnlineResponse: codes.Unavailable, context.Canceled: codes.Canceled, context.DeadlineExceeded: codes.DeadlineExceeded, errors.New("private endpoint"): codes.Unavailable,
	} {
		wrapped := fmt.Errorf("private identity/endpoint: %w", cause)
		h := grpcapi.Handler{OnlineAPI: online.OnlineAPI{AllowOnlineRead: true, Online: onlineService{
			count: func(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnline, error) {
				return domain.InstanceUserOnline{InstanceID: "private"}, wrapped
			},
			ips: func(context.Context, instances.InstanceID, domain.UserEmail) (domain.InstanceUserOnlineIPs, error) {
				return domain.InstanceUserOnlineIPs{IPs: domain.UserOnlineIPs{Items: []domain.OnlineIP{{IP: "private"}}}}, wrapped
			},
			users: func(context.Context, instances.InstanceID) (domain.InstanceOnlineUsers, error) {
				return domain.InstanceOnlineUsers{Users: domain.OnlineUsers{Emails: []domain.UserEmail{"private"}}}, wrapped
			},
		}}}
		count, countError := h.GetUserOnlineStats(testutil.VerifiedGRPCContext(t), &onlinev1.GetUserOnlineStatsRequest{InstanceId: "edge", Email: "reader"})
		ips, ipsError := h.ListUserOnlineIPs(testutil.VerifiedGRPCContext(t), &onlinev1.ListUserOnlineIPsRequest{InstanceId: "edge", Email: "reader"})
		users, usersError := h.ListOnlineUsers(testutil.VerifiedGRPCContext(t), &onlinev1.ListOnlineUsersRequest{InstanceId: "edge"})
		if count != nil || ips != nil || users != nil {
			t.Fatal("failed read returned partial facts")
		}
		for _, err := range []error{countError, ipsError, usersError} {
			if status.Code(err) != code || strings.Contains(err.Error(), "private") {
				t.Fatalf("online failure: %v; want %v", err, code)
			}
		}
	}
}
