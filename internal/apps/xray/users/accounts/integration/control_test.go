//go:build integration && linux

package integration_test

import (
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/protobuf/proto"
)

const managedID = "13dd1692-3aa7-4a42-855a-6b203286c515"

func accountConfiguration(t *testing.T, email string) []byte {
	t.Helper()
	content, err := proto.Marshal(&protocol.User{Email: email, Account: serial.ToTypedMessage(&vless.Account{Id: managedID})})
	if err != nil {
		t.Fatal(err)
	}
	return content
}
func TestLiveAccountAdditionAndRemoval(t *testing.T) {
	pki := testutil.NewPKI(t)
	broker := brokerFixture(t, pki)
	for _, transport := range []string{"http", "grpc", "nats"} {
		t.Run(transport, func(t *testing.T) {
			target := accountTarget(t)
			f := newFixture(t, pki, broker, transport)
			address := allowAccountTraffic(t, f, target.Listener.Addr().String())
			c := startNode(t, f)
			awaitNative(t, f.native)
			exerciseAccountControl(t, c, transport, address, target.Listener.Addr().String())
		})
	}
}
func exerciseAccountControl(t *testing.T, c clients, transport, address, target string) {
	t.Helper()
	content := accountConfiguration(t, "managed@example.test")
	result, code := accountMutation(t, c, transport, "add", "", content)
	if code != "" || !result.Acknowledged || result.Effect != "completed" || result.Email != "managed@example.test" || result.Generation != 1 || result.InboundTag != "members" {
		t.Fatal(result, code)
	}
	var count countResponse
	if err := json.Unmarshal(countData(t, c, transport), &count); err != nil || count.Count != 3 {
		t.Fatal(count, err)
	}
	var read accountResponse
	if err := json.Unmarshal(accountData(t, c, transport, "managed@example.test"), &read); err != nil || read.User == nil {
		t.Fatal(read, err)
	}
	established, reader, err := accountTunnel(address, managedID, target)
	if err != nil {
		t.Fatal("added account did not authenticate", err)
	}
	defer established.Close()
	result, code = accountMutation(t, c, transport, "add", "", content)
	if code == "" || result.Acknowledged || result.Effect != "unknown" || result.RequiresInstanceRestart {
		t.Fatal("duplicate was not a terminal native error", result, code)
	}
	state := accountControl(t, c, transport)
	if state.LastOperation == nil || state.LastOperation.Effect != "unknown" || state.RequiresInstanceRestart {
		t.Fatal(state)
	}
	result, code = accountMutation(t, c, transport, "remove", "MANAGED@example.test", nil)
	if code != "" || !result.Acknowledged || result.Effect != "completed" || result.Email != "MANAGED@example.test" {
		t.Fatal(result, code)
	}
	read = accountResponse{}
	if err := json.Unmarshal(accountData(t, c, transport, "managed@example.test"), &read); err != nil || read.User != nil {
		t.Fatal(read, err)
	}
	// Native VLESS revokes new authentication, but the old stream remains usable.
	established.SetDeadline(time.Now().Add(time.Second))
	if _, err := io.WriteString(established, "GET / HTTP/1.1\r\nHost: fixture\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := accountReply(reader); err != nil {
		t.Fatal("removal unexpectedly killed an established VLESS stream", err)
	}
	if connection, _, err := accountTunnel(address, managedID, target); err == nil {
		connection.Close()
		t.Fatal("removed account still authenticates new traffic")
	}
	result, code = accountMutation(t, c, transport, "remove", "managed@example.test", nil)
	if code == "" || result.Acknowledged || result.RequiresInstanceRestart {
		t.Fatal("missing VLESS user removal was misrepresented", result, code)
	}
}

func TestNativeAnonymousVLESSAccountIsNotAnEmailCount(t *testing.T) {
	target := accountTarget(t)
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	address := allowAccountTraffic(t, f, target.Listener.Addr().String())
	c := startNode(t, f)
	awaitNative(t, f.native)
	result, code := accountMutation(t, c, "http", "add", "", accountConfiguration(t, ""))
	if code != "" || !result.Acknowledged || result.Email != "" {
		t.Fatal(result, code)
	}
	tunnel, _, err := accountTunnel(address, managedID, target.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	var count countResponse
	if err := json.Unmarshal(countData(t, c, "http"), &count); err != nil || count.Count != 2 {
		t.Fatal("native email count unexpectedly includes anonymous credentials", count, err)
	}
	result, code = accountMutation(t, c, "http", "remove", "", nil)
	if code == "" || result.Acknowledged || result.RequiresInstanceRestart {
		t.Fatal("native VLESS empty-email removal was misrepresented", result, code)
	}
}
