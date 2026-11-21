//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/app/proxyman"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/dokodemo"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/finalmask/noise"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

func TestNativeInboundContinuesAfterTimeoutUntilExecutionReplacement(t *testing.T) {
	target, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	f.config.Communications.Requests.Timeout = 150 * time.Millisecond
	c := startNode(t, f)
	awaitNative(t, f.native)
	path := filepath.Join(t.TempDir(), "noise.lua")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	reserved, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.LocalAddr().(*net.UDPAddr)
	port := uint32(address.Port)
	reserved.Close()
	configuration := &core.InboundHandlerConfig{Tag: "late-inbound", ReceiverSettings: serial.ToTypedMessage(&proxyman.ReceiverConfig{Listen: xnet.NewIPOrDomain(xnet.LocalHostIP), PortList: &xnet.PortList{Range: []*xnet.PortRange{{From: port, To: port}}}, StreamSettings: &internet.StreamConfig{Udpmasks: []*serial.TypedMessage{serial.ToTypedMessage(&noise.Config{Script: path})}}}), ProxySettings: serial.ToTypedMessage(&dokodemo.Config{AllowedNetworks: []xnet.Network{xnet.Network_UDP}, RewriteAddress: xnet.NewIPOrDomain(xnet.LocalHostIP), RewritePort: uint32(target.LocalAddr().(*net.UDPAddr).Port)})}
	content, err := proto.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	result, code := nativeMutation(t, c, "http", "add", "", content)
	if code != "deadline_exceeded" || result.Effect != "unknown" || result.Acknowledged || !result.RequiresInstanceRestart {
		t.Fatalf("remote abort falsely inferred %+v %s", result, code)
	}
	state := inboundControl(t, c)
	if !state.RequiresInstanceRestart || state.RequestInFlight || state.LastOperation == nil || state.LastOperation.Effect != "unknown" {
		t.Fatal(state)
	}
	result, code = nativeMutation(t, c, "http", "remove", "late-inbound", nil)
	if code != "restart_required" || result.Effect != "not_applied" || !result.RequiresInstanceRestart {
		t.Fatal(result, code)
	}
	// The pinned noise-mask constructor reads this Lua file synchronously, without
	// the expired request context. Release its real FIFO read; no mock server runs.
	writer, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal("native constructor was not waiting on its input", err)
	}
	file := os.NewFile(uintptr(writer), "owned FIFO writer")
	_, writeErr := io.WriteString(file, "function HandleNoise(...) return nil end\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	connection, err := grpc.NewClient(f.native, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	direct := handlers.NewHandlerServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		probe, stop := context.WithTimeout(ctx, 200*time.Millisecond)
		response, err := direct.ListInbounds(probe, &handlers.ListInboundsRequest{IsOnlyTags: true})
		stop()
		if err == nil && slices.ContainsFunc(response.Inbounds, func(record *core.InboundHandlerConfig) bool { return record.Tag == "late-inbound" }) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native addition never completed after timeout", err)
		case <-ticker.C:
		}
	}
	sender, err := net.DialUDP("udp", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if _, err := sender.Write([]byte("late-effect")); err != nil {
		t.Fatal(err)
	}
	if err := target.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, _, err := target.ReadFromUDP(buffer)
	if err != nil || string(buffer[:n]) != "late-effect" {
		t.Fatalf("late traffic %q %v", buffer[:n], err)
	}
	if !inboundControl(t, c).RequiresInstanceRestart {
		t.Fatal("unobserved late completion cleared recovery requirement")
	}
	request, _ := http.NewRequest("POST", c.address+"/v1/core/engines/instances/edge-a/executions", nil)
	response, err := c.http.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("restart %d %s", response.StatusCode, data)
	}
	awaitNative(t, f.native)
	state = inboundControl(t, c)
	if state.Generation != 2 || state.RequiresInstanceRestart || state.LastOperation == nil || state.LastOperation.Generation != 1 {
		t.Fatal(state)
	}
	_, tags := nativeRecords(t, c, "http", true)
	if slices.Contains(tags, "late-inbound") {
		t.Fatal("late handler survived execution replacement")
	}
}

type controlState struct {
	Generation              uint64     `json:"generation,string"`
	RequestInFlight         bool       `json:"request_in_flight"`
	RequiresInstanceRestart bool       `json:"requires_instance_restart"`
	LastOperation           *operation `json:"last_operation"`
}

func inboundControl(t *testing.T, c clients) controlState {
	t.Helper()
	response, err := c.http.Get(c.address + "/v1/xray/proxies/instances/edge-a/inbounds/control-status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result controlState
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || response.StatusCode != 200 {
		t.Fatal(result, err, response.StatusCode)
	}
	return result
}
