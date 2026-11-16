//go:build integration

package api_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/trojan"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/PapilioVPN/node/internal/modules/xray/domain"
	userdomain "github.com/PapilioVPN/node/internal/modules/xray/users/domain"
)

// Controlled SDK service tests native dispatch/bounds; real Xray managers are
// tested separately. No second worker, cache, count or list-inbound API is needed.
type userObservationServer struct {
	native.UnimplementedHandlerServiceServer
	read func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error)
}

func (s userObservationServer) GetInboundUsers(ctx context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
	return s.read(ctx, r)
}

func observedTestUser(email string) *protocol.User {
	return &protocol.User{Email: email, Level: math.MaxUint32, Account: serial.ToTypedMessage(&trojan.Account{Password: "test-only-native-account"})}
}

func TestSingleUserReadPreservesNativeStoredIdentityAndNeverLists(t *testing.T) {
	user := observedTestUser("mixed@example")
	address, counts := userAPI(t, userObservationServer{read: func(_ context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
		if r.Tag != " exact>>>tag " || r.Email != "Mixed@Example" {
			return nil, status.Error(codes.InvalidArgument, "single read became a list or changed identity")
		}
		return &native.GetInboundUserResponse{Users: []*protocol.User{user}}, nil
	}})
	client := userClient(t, address, time.Second, 4096)
	if _, err := client.ReadRuntimeInfo(t.Context()); err != nil {
		t.Fatal(err)
	}
	beforeRPCs := counts.rpcs.Load()
	selection := userdomain.InboundUserSelection{InboundTag: " exact>>>tag ", Email: "Mixed@Example"}
	before := time.Now()
	result, err := client.ReadInboundUser(t.Context(), selection)
	if err != nil || result.User == nil || result.User.Email != user.Email || result.User.Level != math.MaxUint32 || result.User.Account.TypeName != user.Account.Type || !bytes.Equal(result.User.Account.Protobuf, user.Account.Value) || result.ObservedAt.Before(before) || result.ObservedAt.After(time.Now()) || counts.rpcs.Load() != beforeRPCs+1 || counts.opened.Load() != 1 {
		t.Fatalf("single observation changed data/dispatch/channel: %v", err)
	}
	result.User.Account.Protobuf[0] ^= 0xff
	result, err = client.ReadInboundUser(t.Context(), selection)
	if err != nil || !bytes.Equal(result.User.Account.Protobuf, user.Account.Value) || counts.rpcs.Load() != beforeRPCs+2 {
		t.Fatal("returned account did not have independent native-reply ownership")
	}
	if _, err := client.ReadInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: selection.InboundTag}); !errors.Is(err, userdomain.ErrInvalidUserSelection) || counts.rpcs.Load() != beforeRPCs+2 {
		t.Fatal("empty single email accidentally requested all native users")
	}
}

func TestSingleUserAbsenceIsDistinctFromMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		users   []*protocol.User
		invalid bool
	}{
		{"absent-empty-list", nil, false}, {"absent-native-nil", []*protocol.User{nil}, false}, {"absent-native-empty", []*protocol.User{{}}, false},
		{"missing-account", []*protocol.User{{Email: "user"}}, true},
		{"anonymous-mismatch", []*protocol.User{observedTestUser("")}, true},
		{"ambiguous", []*protocol.User{observedTestUser("one"), observedTestUser("two")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, counts := userAPI(t, userObservationServer{read: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
				return &native.GetInboundUserResponse{Users: tc.users}, nil
			}})
			client := userClient(t, address, time.Second, 4096)
			result, err := client.ReadInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"})
			if result.User != nil || counts.rpcs.Load() != 1 || tc.invalid && (!errors.Is(err, userdomain.ErrInvalidUserObservation) || !result.ObservedAt.IsZero()) || !tc.invalid && (err != nil || result.ObservedAt.IsZero()) {
				t.Fatalf("absence/malformed distinction lost: %v", err)
			}
		})
	}
}

func TestUserListHasOneCallAtEverySizeAndKeepsAllNativeMembers(t *testing.T) {
	for _, count := range []int{0, 1, 100, 1000} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			users := make([]*protocol.User, count)
			for i := range users {
				users[i] = observedTestUser(fmt.Sprint(i % 10))
			}
			if count > 0 {
				users[0] = observedTestUser("")
			}
			address, counts := userAPI(t, userObservationServer{read: func(_ context.Context, r *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
				if r.Tag != " exact>>>tag " || r.Email != "" {
					return nil, status.Error(codes.InvalidArgument, "collection used a single lookup")
				}
				return &native.GetInboundUserResponse{Users: users}, nil
			}})
			client := userClient(t, address, time.Second, 256*1024)
			result, err := client.ReadInboundUsers(t.Context(), " exact>>>tag ")
			if err != nil || len(result.Users) != count || result.ObservedAt.IsZero() || counts.rpcs.Load() != 1 {
				t.Fatalf("collection truncated or acquired extra native work: %v calls=%d", err, counts.rpcs.Load())
			}
			for i, item := range result.Users {
				if item.Email != users[i].Email || item.Level != math.MaxUint32 || !bytes.Equal(item.Account.Protobuf, users[i].Account.Value) {
					t.Fatal("native members or account bytes changed")
				}
			}
		})
	}
}

func TestUserListReceiveLimitOrMalformedMemberNeverReturnsPartialSuccess(t *testing.T) {
	for _, tc := range []struct {
		users []*protocol.User
		limit int
		cause error
	}{
		{[]*protocol.User{observedTestUser("one"), nil}, 4096, userdomain.ErrInvalidUserObservation},
		{[]*protocol.User{observedTestUser("one"), {Email: "bad"}}, 4096, userdomain.ErrInvalidUserObservation},
		{[]*protocol.User{observedTestUser("one"), {Email: "bad", Account: &serial.TypedMessage{Type: "future.account"}}}, 4096, userdomain.ErrUserRecordUnsupported},
		{[]*protocol.User{observedTestUser("one")}, 1, domain.ErrAPIResourceLimit},
	} {
		address, _ := userAPI(t, userObservationServer{read: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
			return &native.GetInboundUserResponse{Users: tc.users}, nil
		}})
		client := userClient(t, address, time.Second, tc.limit)
		result, err := client.ReadInboundUsers(t.Context(), "in")
		if !errors.Is(err, tc.cause) || len(result.Users) != 0 || !result.ObservedAt.IsZero() {
			t.Fatal("failed collection returned partial accounts or lost its native cause")
		}
	}
}

func TestUserObservationsKeepNativeFailureCodesWithoutRetryOrFalseAbsence(t *testing.T) {
	for _, code := range []codes.Code{codes.NotFound, codes.Unimplemented, codes.Unavailable, codes.ResourceExhausted, codes.Canceled, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			address, counts := userAPI(t, userObservationServer{read: func(context.Context, *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
				return nil, status.Error(code, "private diagnostics")
			}})
			client := userClient(t, address, time.Second, 4096)
			one, err := client.ReadInboundUser(t.Context(), userdomain.InboundUserSelection{InboundTag: "in", Email: "user"})
			if status.Code(err) != code || one.User != nil || !one.ObservedAt.IsZero() || counts.rpcs.Load() != 1 {
				t.Fatal("single native failure became absence or retried")
			}
			list, err := client.ReadInboundUsers(t.Context(), "in")
			if status.Code(err) != code || len(list.Users) != 0 || !list.ObservedAt.IsZero() || counts.rpcs.Load() != 2 {
				t.Fatal("collection native failure became empty success or retried")
			}
		})
	}
}

func TestUserObservationSharesAdmissionAndJoinsCanceledCalls(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	address, _ := userAPI(t, userObservationServer{read: func(ctx context.Context, _ *native.GetInboundUserRequest) (*native.GetInboundUserResponse, error) {
		calls.Add(1)
		close(entered)
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	}})
	client := userClient(t, address, time.Second, 4096)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := client.ReadInboundUser(ctx, userdomain.InboundUserSelection{InboundTag: "in", Email: "user"})
		done <- err
	}()
	defer func() { cancel(); _ = client.Close() }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("native observation did not start")
	}
	if _, err := client.ReadInboundUsers(t.Context(), "in"); !errors.Is(err, domain.ErrAPIBusy) {
		t.Fatal("collection bypassed shared admission")
	}
	if _, err := client.ReadInboundUserCount(t.Context(), "in"); !errors.Is(err, domain.ErrAPIBusy) {
		t.Fatal("count bypassed shared admission")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("read lost cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled native read did not join")
	}
	if _, err := client.ReadInboundUsers(ctx, "in"); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatal("pre-canceled collection reached native")
	}
}
