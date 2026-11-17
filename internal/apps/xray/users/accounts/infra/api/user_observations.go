package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	native "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
)

// ReadInboundUser uses the native targeted-email operation exactly once. It never
// lists users to find one. Native email matching and returned stored identity stay
// unchanged. A completed absent lookup still has its own observation timestamp.
func (c *Adapter) ReadInboundUser(ctx context.Context, selection domain.InboundUserSelection) (domain.InboundUserObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.InboundUserObservation{}, err
	}
	// Native empty email selects all users. Guard this protocol discriminator even
	// for internal callers so a single read cannot accidentally become a collection.
	if selection.Email == "" {
		return domain.InboundUserObservation{}, domain.ErrInvalidUserSelection
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.InboundUserObservation{}, err
	}
	defer done()
	client, err := c.API.HandlerClient()
	if err != nil {
		return domain.InboundUserObservation{}, err
	}
	response, err := client.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: selection.InboundTag, Email: selection.Email})
	if cause := ctx.Err(); cause != nil {
		return domain.InboundUserObservation{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InboundUserObservation{}, userObservationError(err)
	}
	if response == nil || len(response.Users) > 1 {
		return domain.InboundUserObservation{}, domain.ErrInvalidUserObservation
	}
	result := domain.InboundUserObservation{}
	// The pinned native handler encodes an absent MemoryUser as one empty protobuf
	// User. An actual named result must carry its account; malformed data is not absence.
	if len(response.Users) == 1 && proto.Size(response.Users[0]) != 0 {
		if response.Users[0].GetEmail() == "" {
			return domain.InboundUserObservation{}, domain.ErrInvalidUserObservation
		}
		user, err := observedUser(response.Users[0])
		if err != nil {
			return domain.InboundUserObservation{}, err
		}
		result.User = &user
	}
	if err := ctx.Err(); err != nil {
		return domain.InboundUserObservation{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

// ReadInboundUsers requests all native members of one inbound once. The configured
// receive ceiling accepts a complete response or fails, never truncates/paginates.
// It does not hard-bound the engine's own scan/allocation or establish a snapshot.
func (c *Adapter) ReadInboundUsers(ctx context.Context, tag string) (domain.InboundUsersObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.InboundUsersObservation{}, err
	}
	ctx, done, err := c.API.Begin(ctx)
	if err != nil {
		return domain.InboundUsersObservation{}, err
	}
	defer done()
	client, err := c.API.HandlerClient()
	if err != nil {
		return domain.InboundUsersObservation{}, err
	}
	response, err := client.GetInboundUsers(ctx, &native.GetInboundUserRequest{Tag: tag})
	if cause := ctx.Err(); cause != nil {
		return domain.InboundUsersObservation{}, errors.Join(cause, err)
	}
	if err != nil {
		return domain.InboundUsersObservation{}, userObservationError(err)
	}
	if response == nil {
		return domain.InboundUsersObservation{}, domain.ErrInvalidUserObservation
	}
	result := domain.InboundUsersObservation{Users: make([]domain.ObservedInboundUser, 0, len(response.Users))}
	for _, value := range response.Users {
		if err := ctx.Err(); err != nil {
			return domain.InboundUsersObservation{}, err
		}
		user, err := observedUser(value)
		if err != nil {
			return domain.InboundUsersObservation{}, err
		}
		result.Users = append(result.Users, user)
	}
	if err := ctx.Err(); err != nil {
		return domain.InboundUsersObservation{}, err
	}
	result.ObservedAt = time.Now().UTC()
	return result, nil
}

// observedUser consumes an owned native reply. The SDK validates the account once;
// original bytes transfer to the result without re-encoding, dropping extensions,
// or copying the same credential payload into another account model.
func observedUser(user *protocol.User) (domain.ObservedInboundUser, error) {
	if user == nil || user.Account == nil || user.Account.Type == "" {
		return domain.ObservedInboundUser{}, domain.ErrInvalidUserObservation
	}
	if len(user.ProtoReflect().GetUnknown()) != 0 {
		return domain.ObservedInboundUser{}, domain.ErrUserRecordUnsupported
	}
	if _, err := user.GetTypedAccount(); err != nil {
		cause := domain.ErrInvalidUserObservation
		if errors.Is(err, protoregistry.NotFound) {
			cause = domain.ErrUserRecordUnsupported
		}
		return domain.ObservedInboundUser{}, errors.Join(cause, err)
	}
	return domain.ObservedInboundUser{Email: user.Email, Level: user.Level, Account: domain.ObservedUserAccount{TypeName: user.Account.Type, Protobuf: user.Account.Value}}, nil
}

func userObservationError(err error) error {
	var cause error
	switch status.Code(err) {
	case codes.NotFound:
		cause = domain.ErrInboundNotFound
	case codes.Unimplemented:
		cause = domain.ErrUserReadUnsupported
	case codes.ResourceExhausted:
		cause = domain.ErrAPIResourceLimit
	case codes.Canceled:
		cause = context.Canceled
	case codes.DeadlineExceeded:
		cause = context.DeadlineExceeded
	}
	return fmt.Errorf("read Xray inbound users: %w", errors.Join(cause, err))
}
