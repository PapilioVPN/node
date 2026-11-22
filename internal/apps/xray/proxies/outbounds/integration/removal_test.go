//go:build integration && linux

package integration_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/PapilioVPN/node/internal/testutil"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/freedom"
	"google.golang.org/protobuf/proto"
)

func TestOutboundMembershipRemovalDoesNotDisconnectEstablishedTunnel(t *testing.T) {
	target, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	f := newFixture(t, testutil.NewPKI(t), "", "http")
	address := configureProbe(t, f, true)
	c := startNode(t, f)
	awaitNative(t, f.native)
	configuration := &core.OutboundHandlerConfig{Tag: "managed", ProxySettings: serial.ToTypedMessage(&freedom.Config{})}
	content, err := proto.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	result, code := nativeMutation(t, c, "http", "add", "", content)
	if code != "" || !result.Acknowledged {
		t.Fatal(result, code)
	}
	client, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target.Addr(), target.Addr()); err != nil {
		t.Fatal(err)
	}
	if err := target.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	upstream, err := target.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	if err := upstream.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(client), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 200 {
		t.Fatal(response, err)
	}
	// Keep the CONNECT body/socket owned by the test until both observations finish.
	roundtrip := func(marker string) {
		t.Helper()
		if _, err := io.WriteString(client, marker); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, len(marker))
		if _, err := io.ReadFull(upstream, data); err != nil || string(data) != marker {
			t.Fatal("existing tunnel was disconnected", string(data), err)
		}
	}
	roundtrip("before-removal")
	result, code = nativeMutation(t, c, "http", "remove", "managed", nil)
	if code != "" || !result.Acknowledged || result.Effect != "completed" {
		t.Fatal(result, code)
	}
	roundtrip("after-removal")
	observation := readOutbounds(t, c, "http")
	for _, record := range observation.Outbounds {
		if record.Tag == "managed" {
			t.Fatal("removed outbound still listed")
		}
	}
}
