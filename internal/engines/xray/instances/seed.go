package instances

import (
	"context"
	"fmt"

	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"github.com/PapilioVPN/node/internal/engines/xray/instances/lifecycle"
	"google.golang.org/grpc"
)

func seedAccounts(ctx context.Context, id string, conn *grpc.ClientConn, groups []lifecycle.InboundUsers, _ []string) (func(), error) {
	if len(groups) == 0 {
		return nil, nil
	}
	service := membership.New(native.New(candidateConnection{conn}))
	for _, group := range groups {
		result, err := service.SyncUsers(ctx, membership.SyncUsersRequest{
			Scope:    membership.Scope{InstanceID: id, InboundTag: group.InboundTag},
			Accounts: group.Accounts,
		})
		if err != nil {
			return nil, err
		}
		if result.Outcome != membership.Confirmed {
			return nil, fmt.Errorf("seed accounts: %s (%s)", result.Outcome, result.Failure)
		}
	}
	return nil, nil
}

// The accounts adapter may use this connection while the candidate is still
// private. Its lifetime remains owned by the lifecycle service.
type candidateConnection struct{ conn *grpc.ClientConn }

func (c candidateConnection) NativeConnection(string) (*grpc.ClientConn, error) { return c.conn, nil }
