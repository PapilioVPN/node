//go:build integration && linux

package integration_test

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// allowAccountTraffic changes only the isolated fixture's baseline outbound.
func allowAccountTraffic(t *testing.T, f fixture, target string) string {
	t.Helper()
	content, err := os.ReadFile(f.config.Instances[0].Xray.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	var configuration map[string]any
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatal(err)
	}
	inbound := configuration["inbounds"].([]any)[0].(map[string]any)
	address := fmt.Sprintf("127.0.0.1:%d", int(inbound["port"].(float64)))
	outbound := configuration["outbounds"].([]any)[0].(map[string]any)
	outbound["protocol"] = "freedom"
	_, port, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	// The native JSON builder blocks private targets by default. Permit only the
	// isolated test server, retaining the default policy for every other target.
	outbound["settings"] = map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": port}}}
	content, err = json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.config.Instances[0].Xray.Configuration, content, 0600); err != nil {
		t.Fatal(err)
	}
	return address
}
func accountTarget(t *testing.T) *httptest.Server {
	t.Helper()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		io.WriteString(w, "pong")
	}))
	t.Cleanup(target.Close)
	return target
}
func accountTunnel(address, id, target string) (net.Conn, *bufio.Reader, error) {
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (net.Conn, *bufio.Reader, error) { connection.Close(); return nil, nil, err }
	connection.SetDeadline(time.Now().Add(time.Second))
	uuid, err := hex.DecodeString(strings.ReplaceAll(id, "-", ""))
	if err != nil || len(uuid) != 16 {
		return fail(fmt.Errorf("invalid fixture UUID"))
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return fail(err)
	}
	var number uint16
	if _, err := fmt.Sscan(port, &number); err != nil {
		return fail(err)
	}
	header := append([]byte{0}, uuid...)
	header = append(header, 0, 1)
	header = binary.BigEndian.AppendUint16(header, number)
	header = append(header, 1)
	header = append(header, net.ParseIP(host).To4()...)
	header = append(header, []byte("GET / HTTP/1.1\r\nHost: fixture\r\n\r\n")...)
	if _, err := connection.Write(header); err != nil {
		return fail(err)
	}
	reader := bufio.NewReader(connection)
	var response [2]byte
	if _, err := io.ReadFull(reader, response[:]); err != nil {
		return fail(err)
	}
	if response != [2]byte{0, 0} {
		return fail(fmt.Errorf("invalid VLESS response"))
	}
	if err := accountReply(reader); err != nil {
		return fail(err)
	}
	return connection, reader, nil
}
func accountReply(reader *bufio.Reader) error {
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	content, err := io.ReadAll(io.LimitReader(response.Body, 32))
	if err != nil {
		return err
	}
	if response.StatusCode != 200 || string(content) != "pong" {
		return fmt.Errorf("fixture traffic failed")
	}
	return nil
}
