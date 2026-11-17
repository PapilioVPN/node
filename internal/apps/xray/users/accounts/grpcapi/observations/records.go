package observations

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	instances "github.com/PapilioVPN/node/internal/apps/core/engines/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/validators"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	userv1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"github.com/PapilioVPN/node/internal/shared/grpcidentity"
	"github.com/PapilioVPN/node/internal/shared/grpcresponse"
)

type InboundUserReader interface {
	GetInboundUser(context.Context, instances.InstanceID, domain.InboundUserSelection) (domain.InstanceInboundUserObservation, error)
	ListInboundUsers(context.Context, instances.InstanceID, string) (domain.InstanceInboundUsersObservation, error)
}

func (h ObservationAPI) GetInboundUserMetadata(ctx context.Context, request *userv1.GetInboundUserMetadataRequest) (*userv1.GetInboundUserMetadataResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserMetadataRead {
		return nil, status.Error(codes.PermissionDenied, "engine user metadata read is not granted")
	}
	selection := domain.InboundUserSelection{InboundTag: request.GetInboundTag(), Email: request.GetEmail()}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundUserSelection(selection) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id, inbound_tag and email are required")
	}
	result, err := h.Users.GetInboundUser(ctx, instances.InstanceID(request.InstanceId), selection)
	if err := userObservationReadError(ctx, err); err != nil {
		return nil, err
	}
	response := &userv1.GetInboundUserMetadataResponse{InstanceId: string(result.InstanceID), InboundTag: result.Selection.InboundTag, Email: result.Selection.Email, ObservedAtUnixMs: result.Observation.ObservedAt.UnixMilli()}
	if user := result.Observation.User; user != nil {
		response.User = userMetadataResponse(*user)
	}
	return response, nil
}

func (h ObservationAPI) ListInboundUserMetadata(ctx context.Context, request *userv1.ListInboundUserMetadataRequest) (*userv1.ListInboundUserMetadataResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserMetadataRead {
		return nil, status.Error(codes.PermissionDenied, "engine user metadata read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundTag(request.GetInboundTag()) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id and inbound_tag are required")
	}
	result, err := h.Users.ListInboundUsers(ctx, instances.InstanceID(request.InstanceId), request.InboundTag)
	if err := userObservationReadError(ctx, err); err != nil {
		return nil, err
	}
	users := make([]*userv1.InboundUserMetadata, len(result.Observation.Users))
	for i, user := range result.Observation.Users {
		if err := ctx.Err(); err != nil {
			return nil, userObservationReadError(ctx, err)
		}
		users[i] = userMetadataResponse(user)
	}
	if err := ctx.Err(); err != nil {
		return nil, userObservationReadError(ctx, err)
	}
	return &userv1.ListInboundUserMetadataResponse{InstanceId: string(result.InstanceID), InboundTag: result.InboundTag, Users: users, ObservedAtUnixMs: result.Observation.ObservedAt.UnixMilli()}, nil
}

func (h ObservationAPI) GetInboundUser(ctx context.Context, request *userv1.GetInboundUserRequest) (*userv1.GetInboundUserResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserAccountsRead {
		return nil, status.Error(codes.PermissionDenied, "engine user account read is not granted")
	}
	selection := domain.InboundUserSelection{InboundTag: request.GetInboundTag(), Email: request.GetEmail()}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundUserSelection(selection) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id, inbound_tag and email are required")
	}
	result, err := h.Users.GetInboundUser(ctx, instances.InstanceID(request.InstanceId), selection)
	if err := userObservationReadError(ctx, err); err != nil {
		return nil, err
	}
	response := &userv1.GetInboundUserResponse{InstanceId: string(result.InstanceID), InboundTag: result.Selection.InboundTag, Email: result.Selection.Email, ObservedAtUnixMs: result.Observation.ObservedAt.UnixMilli()}
	if user := result.Observation.User; user != nil {
		response.User = userRecordResponse(*user)
	}
	return response, nil
}

func (h ObservationAPI) ListInboundUsers(ctx context.Context, request *userv1.ListInboundUsersRequest) (*userv1.ListInboundUsersResponse, error) {
	if err := grpcidentity.VerifyClient(ctx); err != nil {
		return nil, err
	}
	if !h.AllowUserAccountsRead {
		return nil, status.Error(codes.PermissionDenied, "engine user account read is not granted")
	}
	if strings.TrimSpace(request.GetInstanceId()) == "" || validators.InboundTag(request.GetInboundTag()) != nil {
		return nil, status.Error(codes.InvalidArgument, "instance_id and inbound_tag are required")
	}
	result, err := h.Users.ListInboundUsers(ctx, instances.InstanceID(request.InstanceId), request.InboundTag)
	if err := userObservationReadError(ctx, err); err != nil {
		return nil, err
	}
	users := make([]*userv1.InboundUserRecord, len(result.Observation.Users))
	for i, user := range result.Observation.Users {
		if err := ctx.Err(); err != nil {
			return nil, userObservationReadError(ctx, err)
		}
		users[i] = userRecordResponse(user)
	}
	if err := ctx.Err(); err != nil {
		return nil, userObservationReadError(ctx, err)
	}
	return &userv1.ListInboundUsersResponse{InstanceId: string(result.InstanceID), InboundTag: result.InboundTag, Users: users, ObservedAtUnixMs: result.Observation.ObservedAt.UnixMilli()}, nil
}

func userMetadataResponse(user domain.ObservedInboundUser) *userv1.InboundUserMetadata {
	return &userv1.InboundUserMetadata{Email: user.Email, Level: user.Level, AccountType: user.Account.TypeName}
}

func userRecordResponse(user domain.ObservedInboundUser) *userv1.InboundUserRecord {
	return &userv1.InboundUserRecord{Email: user.Email, Level: user.Level, Account: &userv1.NativeUserAccount{TypeName: user.Account.TypeName, Protobuf: user.Account.Protobuf}}
}

func userObservationReadError(ctx context.Context, err error) error {
	if mapped := grpcresponse.ContextError(errors.Join(ctx.Err(), err)); mapped != nil {
		return mapped
	}
	switch {
	case errors.Is(err, instances.ErrNotFound):
		return status.Error(codes.NotFound, "Xray instance not found")
	case errors.Is(err, domain.ErrInboundNotFound):
		return status.Error(codes.NotFound, "Xray inbound not found")
	case errors.Is(err, domain.ErrAPIDisabled):
		return status.Error(codes.FailedPrecondition, "Xray API is not enabled")
	case errors.Is(err, domain.ErrUserReadUnsupported), errors.Is(err, domain.ErrUserRecordUnsupported):
		return status.Error(codes.FailedPrecondition, "Xray user observations are not supported")
	case errors.Is(err, domain.ErrAPIBusy), errors.Is(err, domain.ErrAPIResourceLimit):
		return status.Error(codes.ResourceExhausted, "Xray API resource limit exceeded")
	case err != nil:
		return status.Error(codes.Unavailable, "cannot read Xray inbound users")
	default:
		return nil
	}
}
