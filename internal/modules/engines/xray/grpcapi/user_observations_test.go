package grpcapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	instances "github.com/PapilioVPN/node/internal/modules/core/instances/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/domain"
	"github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi"
	xrayv1 "github.com/PapilioVPN/node/internal/modules/engines/xray/grpcapi/proto/v1"
)

func TestUserObservationIdentityPrecedesShapeAndSelection(t *testing.T) {
	h := grpcapi.Handler{}
	_, e1 := h.GetInboundUserMetadata(t.Context(), nil)
	_, e2 := h.ListInboundUserMetadata(t.Context(), nil)
	_, e3 := h.GetInboundUser(t.Context(), nil)
	_, e4 := h.ListInboundUsers(t.Context(), nil)
	for i, err := range []error{e1, e2, e3, e4} {
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("operation %d bypassed identity: %v", i, err)
		}
	}
}

type userObservationReader struct {
	one  func(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error)
	list func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUsersObservation, error)
}

func (r userObservationReader) GetInboundUser(ctx context.Context, id instances.InstanceID, s domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error) {
	return r.one(ctx, id, s)
}
func (r userObservationReader) ListInboundUsers(ctx context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUsersObservation, error) {
	return r.list(ctx, id, tag)
}

// Every closure uses a valid request so a failure cannot hide behind shape checks.
var userReadCalls = []struct {
	name string
	call func(context.Context, grpcapi.Handler) (proto.Message, error)
}{
	{"metadata-single", func(ctx context.Context, h grpcapi.Handler) (proto.Message, error) {
		return h.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
	}},
	{"metadata-list", func(ctx context.Context, h grpcapi.Handler) (proto.Message, error) {
		return h.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
	}},
	{"account-single", func(ctx context.Context, h grpcapi.Handler) (proto.Message, error) {
		return h.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
	}},
	{"account-list", func(ctx context.Context, h grpcapi.Handler) (proto.Message, error) {
		return h.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
	}},
}

func TestUserViewsRequireIndependentGrants(t *testing.T) {
	for i, op := range userReadCalls {
		t.Run(op.name, func(t *testing.T) {
			h := grpcapi.Handler{AllowUsersRead: true, AllowUserManagement: true, AllowInfoRead: true, AllowStatsRead: true, AllowStatsReset: true, AllowOnlineRead: true, AllowRoutingRead: true, AllowRoutingTest: true, AllowRoutingManagement: true, AllowOutboundsRead: true}
			// Also grant the opposite view: it must not enable this operation.
			if i < 2 {
				h.AllowUserAccountsRead = true
			} else {
				h.AllowUserMetadataRead = true
			}
			r, err := op.call(verifiedContext(t), h)
			if r.ProtoReflect().IsValid() || status.Code(err) != codes.PermissionDenied {
				t.Fatalf("other grants enabled %s: %v", op.name, err)
			}
		})
	}
}

func TestUserViewsRejectMalformedSelectionBeforeApplication(t *testing.T) {
	h := grpcapi.Handler{AllowUserMetadataRead: true, AllowUserAccountsRead: true}
	ctx := verifiedContext(t)
	for _, id := range []string{"", " "} {
		_, e1 := h.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: id, InboundTag: "in", Email: "user"})
		_, e2 := h.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: id, InboundTag: "in"})
		_, e3 := h.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: id, InboundTag: "in", Email: "user"})
		_, e4 := h.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: id, InboundTag: "in"})
		for _, e := range []error{e1, e2, e3, e4} {
			if status.Code(e) != codes.InvalidArgument {
				t.Fatalf("invalid instance reached port: %v", e)
			}
		}
	}
	for _, tag := range []string{"", " ", "\xff"} {
		_, e1 := h.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: "edge", InboundTag: tag, Email: "user"})
		_, e2 := h.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: "edge", InboundTag: tag})
		_, e3 := h.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: "edge", InboundTag: tag, Email: "user"})
		_, e4 := h.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: "edge", InboundTag: tag})
		for _, e := range []error{e1, e2, e3, e4} {
			if status.Code(e) != codes.InvalidArgument {
				t.Fatalf("invalid tag reached port: %v", e)
			}
		}
	}
	for _, email := range []string{"", " ", "\xff"} {
		_, e1 := h.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: "edge", InboundTag: "in", Email: email})
		_, e2 := h.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: "edge", InboundTag: "in", Email: email})
		for _, e := range []error{e1, e2} {
			if status.Code(e) != codes.InvalidArgument {
				t.Fatalf("invalid email reached port: %v", e)
			}
		}
	}
	_, e1 := h.GetInboundUserMetadata(ctx, nil)
	_, e2 := h.ListInboundUserMetadata(ctx, nil)
	_, e3 := h.GetInboundUser(ctx, nil)
	_, e4 := h.ListInboundUsers(ctx, nil)
	for _, e := range []error{e1, e2, e3, e4} {
		if status.Code(e) != codes.InvalidArgument {
			t.Fatalf("nil reached application: %v", e)
		}
	}
}

func TestSingleUserViewsPreserveRequestedAndStoredIdentityAndAbsence(t *testing.T) {
	observed := time.UnixMilli(1700000000123)
	payload := []byte("test-only-secret-and-unknown-native-fields")
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			ctx := verifiedContext(t)
			calls := 0
			h := grpcapi.Handler{AllowUserMetadataRead: true, AllowUserAccountsRead: true, Users: userObservationReader{one: func(c context.Context, id instances.InstanceID, s domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error) {
				calls++
				if c != ctx || id != " edge " || s.InboundTag != " exact>>>tag " || s.Email != " Mixed@Example " {
					t.Fatal("single selection/context changed")
				}
				result := domain.InstanceInboundUserObservation{InstanceID: id, Selection: s, Observation: domain.InboundUserObservation{ObservedAt: observed}}
				if present {
					result.Observation.User = &domain.ObservedInboundUser{Email: " stored@example ", Level: math.MaxUint32, Account: domain.ObservedUserAccount{TypeName: "native.Account", Protobuf: payload}}
				}
				return result, nil
			}}}
			metadata, err := h.GetInboundUserMetadata(ctx, &xrayv1.GetInboundUserMetadataRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
			if err != nil || calls != 1 || metadata.InstanceId != " edge " || metadata.InboundTag != " exact>>>tag " || metadata.Email != " Mixed@Example " || metadata.ObservedAtUnixMs != observed.UnixMilli() || (metadata.User != nil) != present {
				t.Fatal("metadata selection/absence/time changed")
			}
			encoded, err := proto.Marshal(metadata)
			if err != nil || bytes.Contains(encoded, payload) {
				t.Fatal("metadata disclosed the account payload")
			}
			full, err := h.GetInboundUser(ctx, &xrayv1.GetInboundUserRequest{InstanceId: " edge ", InboundTag: " exact>>>tag ", Email: " Mixed@Example "})
			if err != nil || calls != 2 || full.InstanceId != metadata.InstanceId || full.InboundTag != metadata.InboundTag || full.Email != metadata.Email || full.ObservedAtUnixMs != metadata.ObservedAtUnixMs || (full.User != nil) != present {
				t.Fatal("full record selection/absence/time changed")
			}
			if present {
				encoded, err = proto.Marshal(full)
				var decoded xrayv1.GetInboundUserResponse
				if err != nil || proto.Unmarshal(encoded, &decoded) != nil || decoded.User.Email != " stored@example " || decoded.User.Level != math.MaxUint32 || decoded.User.Account.TypeName != "native.Account" || !bytes.Equal(decoded.User.Account.Protobuf, payload) || metadata.User.Email != decoded.User.Email || metadata.User.Level != decoded.User.Level || metadata.User.AccountType != decoded.User.Account.TypeName {
					t.Fatal("wire lost stored identity/uint32/native account bytes")
				}
			}
		})
	}
}

func TestUserListsKeepAllMembersAndUseOneApplicationCall(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := verifiedContext(t)
			calls := 0
			observed := time.UnixMilli(1700000000123)
			users := make([]domain.ObservedInboundUser, count)
			for i := range users {
				users[i] = domain.ObservedInboundUser{Email: fmt.Sprint(i % 3), Level: math.MaxUint32, Account: domain.ObservedUserAccount{TypeName: "native.Account", Protobuf: []byte(fmt.Sprintf("test-only-secret-%d", i))}}
			}
			if count > 0 {
				users[0].Email = ""
			}
			h := grpcapi.Handler{AllowUserMetadataRead: true, AllowUserAccountsRead: true, Users: userObservationReader{list: func(c context.Context, id instances.InstanceID, tag string) (domain.InstanceInboundUsersObservation, error) {
				calls++
				if c != ctx || id != " edge " || tag != " exact>>>tag " {
					t.Fatal("list selection/context changed")
				}
				return domain.InstanceInboundUsersObservation{InstanceID: id, InboundTag: tag, Observation: domain.InboundUsersObservation{Users: users, ObservedAt: observed}}, nil
			}}}
			metadata, err := h.ListInboundUserMetadata(ctx, &xrayv1.ListInboundUserMetadataRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
			if err != nil || calls != 1 || len(metadata.Users) != count || metadata.InstanceId != " edge " || metadata.InboundTag != " exact>>>tag " || metadata.ObservedAtUnixMs != observed.UnixMilli() {
				t.Fatal("metadata list lost members/selection/time or refetched")
			}
			encoded, err := proto.Marshal(metadata)
			if err != nil || bytes.Contains(encoded, []byte("test-only-secret-")) {
				t.Fatal("metadata list disclosed native payload")
			}
			full, err := h.ListInboundUsers(ctx, &xrayv1.ListInboundUsersRequest{InstanceId: " edge ", InboundTag: " exact>>>tag "})
			if err != nil || calls != 2 || len(full.Users) != count || full.InstanceId != metadata.InstanceId || full.InboundTag != metadata.InboundTag || full.ObservedAtUnixMs != metadata.ObservedAtUnixMs {
				t.Fatal("full list lost members/selection/time or refetched")
			}
			encoded, err = proto.Marshal(full)
			var decoded xrayv1.ListInboundUsersResponse
			if err != nil || proto.Unmarshal(encoded, &decoded) != nil {
				t.Fatal("full list failed wire roundtrip")
			}
			for i, user := range users {
				m, r := metadata.Users[i], decoded.Users[i]
				if m.Email != user.Email || m.Level != user.Level || m.AccountType != user.Account.TypeName || r.Email != user.Email || r.Level != user.Level || r.Account.TypeName != user.Account.TypeName || !bytes.Equal(r.Account.Protobuf, user.Account.Protobuf) {
					t.Fatal("list sorted/dropped duplicates, anonymous members or native fields")
				}
			}
		})
	}
}

func TestUserReadFailuresAreSanitizedWithoutPartialAccounts(t *testing.T) {
	for _, tc := range []struct {
		cause error
		code  codes.Code
	}{
		{instances.ErrNotFound, codes.NotFound}, {domain.ErrInboundNotFound, codes.NotFound},
		{domain.ErrAPIDisabled, codes.FailedPrecondition}, {domain.ErrUserReadUnsupported, codes.FailedPrecondition}, {domain.ErrUserRecordUnsupported, codes.FailedPrecondition},
		{domain.ErrAPIBusy, codes.ResourceExhausted}, {domain.ErrAPIResourceLimit, codes.ResourceExhausted},
		{domain.ErrInvalidUserObservation, codes.Unavailable}, {errors.New("native failure"), codes.Unavailable},
		{context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
	} {
		for _, op := range userReadCalls {
			t.Run(op.name+"/"+tc.cause.Error(), func(t *testing.T) {
				cause := errors.Join(tc.cause, errors.New("private-account-secret"))
				h := grpcapi.Handler{AllowUserMetadataRead: true, AllowUserAccountsRead: true, Users: userObservationReader{
					one: func(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error) {
						return domain.InstanceInboundUserObservation{Observation: domain.InboundUserObservation{User: &domain.ObservedInboundUser{Email: "partial"}}}, cause
					},
					list: func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUsersObservation, error) {
						return domain.InstanceInboundUsersObservation{Observation: domain.InboundUsersObservation{Users: []domain.ObservedInboundUser{{Email: "partial"}}}}, cause
					},
				}}
				response, err := op.call(verifiedContext(t), h)
				if response.ProtoReflect().IsValid() || status.Code(err) != tc.code || strings.Contains(err.Error(), "private") {
					t.Fatalf("failed read returned partial data or private causes: %v", err)
				}
			})
		}
	}
	for _, op := range userReadCalls {
		t.Run(op.name+"/late-cancel", func(t *testing.T) {
			ctx, cancel := context.WithCancel(verifiedContext(t))
			defer cancel()
			h := grpcapi.Handler{AllowUserMetadataRead: true, AllowUserAccountsRead: true, Users: userObservationReader{
				one: func(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error) {
					cancel()
					return domain.InstanceInboundUserObservation{}, nil
				},
				list: func(context.Context, instances.InstanceID, string) (domain.InstanceInboundUsersObservation, error) {
					cancel()
					return domain.InstanceInboundUsersObservation{}, nil
				},
			}}
			response, err := op.call(ctx, h)
			if response.ProtoReflect().IsValid() || status.Code(err) != codes.Canceled {
				t.Fatal("late cancellation became successful read")
			}
		})
	}
}
