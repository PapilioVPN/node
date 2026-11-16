// Package grpcapi adapts user operations to the existing versioned wire contract.
package grpcapi

import xrayv1 "github.com/PapilioVPN/node/internal/bootstrap/xrayapi/proto/v1"

// UserAPI is composed into the existing XrayService registration. Each operation
// owns its focused application port; the engine server owns no user workflows.
type UserAPI struct {
	xrayv1.UnimplementedXrayServiceServer
	UserRemoval           InboundUserRemover
	UserAddition          InboundUserAdder
	UserCounts            InboundUserCountReader
	Online                OnlineReader
	AllowOnlineRead       bool
	Users                 InboundUserReader
	AllowUserManagement   bool
	AllowUsersRead        bool
	AllowUserMetadataRead bool
	AllowUserAccountsRead bool
}
