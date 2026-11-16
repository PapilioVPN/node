package domain

import (
	"errors"

	"github.com/PapilioVPN/node/internal/shared/engineapi"
)

var ErrAPIDisabled = engineapi.ErrDisabled
var ErrAPIBusy = engineapi.ErrBusy
var ErrAPIResourceLimit = engineapi.ErrResourceLimit

var ErrInvalidUserSelection = errors.New("invalid Xray user selection")
var ErrUserManagementUnsupported = errors.New("Xray user management is not supported")
var ErrUserMutationUnknown = errors.New("Xray user mutation outcome is unknown")
var ErrInvalidUserAccount = errors.New("invalid Xray user account")
var ErrInboundNotFound = errors.New("Xray inbound not found")
var ErrInboundProtocolMismatch = errors.New("Xray inbound protocol does not match account")
var ErrInvalidInboundResponse = errors.New("invalid Xray inbound response")
var ErrInvalidInboundTag = errors.New("invalid Xray inbound tag")
var ErrUserReadUnsupported = errors.New("Xray user observations are not supported")
var ErrInvalidUserCount = errors.New("invalid Xray user count")
var ErrInvalidUserObservation = errors.New("invalid Xray user observation")
var ErrUserRecordUnsupported = errors.New("unsupported Xray native user record")
var ErrUserCollectionsDisabled = errors.New("Xray user collection mutations are not configured")
var ErrUserCollectionTooLarge = errors.New("Xray user collection exceeds its configured limit")
