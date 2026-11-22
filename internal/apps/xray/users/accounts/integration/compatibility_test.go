//go:build integration && linux

package integration_test

import (
	"encoding/json"
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

func TestWrongAccountTypeIsRejectedWithoutNativeEffectAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			address, relay := accountRelay(t, f.native)
			f.config.Instances[0].Xray.API.Address = address
			if f.config.Communications.Requests.NATS != nil {
				f.config.Communications.Requests.NATS.PendingBytes = 2 << 20
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			raw, _ := proto.Marshal(&protocol.User{Email: "wrong@example.test", Account: &serial.TypedMessage{Type: "xray.proxy.trojan.Account", Value: []byte("private-credential")}})
			before := relay.alters.Load()
			result, code := accountMutation(t, c, transport, "add", "", raw)
			if code != "incompatible_account" || result.Effect != "not_applied" || result.Acknowledged || relay.alters.Load() != before {
				t.Fatal("foreign account reached engine", result, code)
			}
			data := compatibilityData(t, c, transport, "compatibility", "members", "xray.proxy.trojan.Account")
			var mismatch struct {
				Compatible *bool `json:"compatible"`
				Guidance   struct {
					Next string `json:"next_action"`
				} `json:"guidance"`
			}
			if err := json.Unmarshal(data, &mismatch); err != nil || mismatch.Compatible == nil || *mismatch.Compatible || mismatch.Guidance.Next != "use_expected_account_type" {
				t.Fatal(string(data), err)
			}
			assertFamilyCount(t, c, transport, 2)
		})
	}
}

func TestMaximumLiveBatchUsesOneCompatibilityReadAcrossTransports(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			f := newFixture(t, pki, broker, transport)
			address, relay := accountRelay(t, f.native)
			f.config.Instances[0].Xray.API.Address = address
			f.config.Communications.Requests.Timeout = 2 * time.Second
			if f.config.Communications.HTTP != nil {
				f.config.Communications.HTTP.BodyLimit = 2 << 20
				f.config.Communications.HTTP.WriteTimeout = 3 * time.Second
			}
			if f.config.Communications.GRPC != nil {
				f.config.Communications.GRPC.MaxRecvMessageSize = 2 << 20
				f.config.Communications.GRPC.MaxSendMessageSize = 2 << 20
			}
			if f.config.Communications.Requests.NATS != nil {
				f.config.Communications.Requests.NATS.PendingBytes = 2 << 20
			}
			c := startNode(t, f)
			awaitNative(t, f.native)
			additions := make([]domain.Addition, domain.MaxBatchItems)
			removals := make([]domain.Removal, domain.MaxBatchItems)
			for i := range additions {
				email := fmt.Sprintf("maximum-%d@example.test", i)
				raw, _ := proto.Marshal(&protocol.User{Email: email, Account: serial.ToTypedMessage(&vless.Account{Id: fmt.Sprintf("00000000-0000-4000-8000-%012x", i+2000)})})
				additions[i] = domain.Addition{InboundTag: "members", Configuration: raw}
				removals[i] = domain.Removal{InboundTag: "members", Email: email}
			}
			result := batchAccounts(t, c, transport, additions, nil, 4)
			if result.Error != "" || result.Operation.Effect != "completed" || len(result.Items) != 1000 || relay.lists.Load() != 1 || relay.alters.Load() != 1000 {
				t.Fatal("maximum batch failed", result.Operation, result.Error, relay.lists.Load(), relay.alters.Load())
			}
			assertFamilyCount(t, c, transport, 1002)
			result = batchAccounts(t, c, transport, nil, removals, 4)
			if result.Error != "" || result.Operation.Effect != "completed" || relay.alters.Load() != 2000 {
				t.Fatal(result.Operation, result.Error, relay.alters.Load())
			}
			assertFamilyCount(t, c, transport, 2)
		})
	}
}
