//go:build integration && linux

package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/proto"
)

func TestLiveAccountBatchesAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := accountTarget(t)
			f := newFixture(t, pki, broker, transport)
			address := allowAccountTraffic(t, f, target.Listener.Addr().String())
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if h := f.config.Communications.HTTP; h != nil {
				h.BodyLimit, h.WriteTimeout = 1<<20, 3*time.Second
			}
			if g := f.config.Communications.GRPC; g != nil {
				g.MaxRecvMessageSize, g.MaxSendMessageSize = 1<<20, 1<<20
			}
			if n := f.config.Communications.Requests.NATS; n != nil {
				n.PendingBytes = 1 << 20
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			var additions []domain.Addition
			var removals []domain.Removal
			for i := range 51 {
				email, id := fmt.Sprintf("batch-%d@example.test", i), fmt.Sprintf("00000000-0000-4000-8000-%012x", i+100)
				data, err := proto.Marshal(&protocol.User{Email: email, Account: serial.ToTypedMessage(&vless.Account{Id: id})})
				if err != nil {
					t.Fatal(err)
				}
				additions = append(additions, domain.Addition{InboundTag: "members", Configuration: data})
				removals = append(removals, domain.Removal{InboundTag: "members", Email: email})
			}
			result := batchAccounts(t, c, transport, additions, nil, 4)
			if result.Error != "" || len(result.Items) != 51 || result.Operation.Effect != "completed" || !result.Operation.Acknowledged {
				t.Fatal(result)
			}
			for i, item := range result.Items {
				if item.Index != i || !item.Attempted || item.Error != "" || item.Operation.Email != removals[i].Email || !item.Operation.Acknowledged {
					t.Fatal(i, item)
				}
			}
			assertFamilyCount(t, c, transport, 53)
			if member := familyMember(t, c, transport, removals[50].Email); member == nil {
				t.Fatal("last native batch member is absent")
			}
			id := fmt.Sprintf("00000000-0000-4000-8000-%012x", 150)
			connection, _, err := accountTunnel(address, id, target.Listener.Addr().String())
			if err != nil {
				t.Fatal("batch-added account did not authenticate", err)
			}
			connection.Close()
			result = batchAccounts(t, c, transport, nil, removals, 4)
			if result.Error != "" || len(result.Items) != 51 || result.Operation.Effect != "completed" {
				t.Fatal(result)
			}
			assertFamilyCount(t, c, transport, 2)
			if connection, _, err := accountTunnel(address, id, target.Listener.Addr().String()); err == nil {
				connection.Close()
				t.Fatal("batch-removed account still authenticates")
			}
			// Malformed protobuf fails locally after the first ordered effect;
			// the following item is never attempted. No atomicity is implied.
			partial := make([]domain.Addition, 1000)
			for i := range partial {
				partial[i] = additions[1]
			}
			partial[0], partial[1] = additions[0], domain.Addition{InboundTag: "members", Configuration: []byte{255}}
			result = batchAccounts(t, c, transport, partial, nil, 1)
			if result.Error != "invalid_argument" || result.Operation.Effect != "partial" || result.Operation.Acknowledged || len(result.Items) != 1000 || result.Items[0].Operation.Effect != "completed" || !result.Items[1].Attempted || result.Items[1].Operation.Effect != "not_applied" || result.Items[2].Attempted || result.Items[2].Error != "not_attempted" {
				t.Fatal(result)
			}
			assertFamilyCount(t, c, transport, 3)
			if state := accountControl(t, c, transport); state.RequiresInstanceRestart || state.LastOperation == nil || state.LastOperation.Kind != "add_batch" || state.LastOperation.Effect != "partial" {
				t.Fatal(state)
			}
			result = batchAccounts(t, c, transport, nil, removals[:1], 1)
			if result.Error != "" {
				t.Fatal(result)
			}
			assertFamilyCount(t, c, transport, 2)
		})
	}
}
