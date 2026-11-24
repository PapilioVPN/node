package online

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

const onlineSource = "127.0.0.2" // Xray omits 127.0.0.1 from its online IP map.

func OpenTunnel(ctx context.Context, proxy, target, name string) (_ net.Conn, err error) {
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(onlineSource)}}
	conn, err := dialer.DialContext(ctx, "tcp4", proxy)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	auth := base64.StdEncoding.EncodeToString([]byte(name + ":fixture"))
	_, err = fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: Basic %s\r\n\r\n", target, target, auth)
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	reply, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	if reply.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("proxy CONNECT returned %s", reply.Status)
	}
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: %s\r\n\r\n", target); err != nil {
		return nil, err
	}
	reply, err = http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(reply.Body)
	reply.Body.Close()
	if err != nil {
		return nil, err
	}
	if reply.StatusCode != http.StatusOK || string(body) != "traffic fixture" {
		return nil, fmt.Errorf("online tunnel returned %s %q", reply.Status, body)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	return conn, nil
}

func HTTP(client *http.Client, address string, scope, name string, reference bool) fixture.ReadCall {
	path := "/v1/xray/instances/primary/stats/online/ips?name=" + url.QueryEscape(name)
	if scope == "online-count" {
		path = "/v1/xray/instances/primary/stats/online/count?name=" + url.QueryEscape(name)
	}
	var payload []byte
	decode := func(body []byte, response proto.Message) error { return json.Unmarshal(body, response) }
	if reference {
		path = "/stats/user/online_ip"
		if scope == "online-count" {
			path = "/stats/user/online"
		}
		var err error
		payload, err = proto.Marshal(&fixture.ReferenceTrafficRequest{Name: name})
		fixture.Check(err)
		decode = proto.Unmarshal
	}
	endpoint := "https://" + address + path
	return validateOnlineRead(scope, name, func(ctx context.Context, response proto.Message) error {
		body, err := fixture.HTTPRead(ctx, client, endpoint, payload)
		if err != nil {
			return err
		}
		return decode(body, response)
	})
}

func GRPC(conn *grpc.ClientConn, scope, name string, reference bool) fixture.ReadCall {
	method := statsv1.XrayStatsService_ListOnlineIPs_FullMethodName
	if scope == "online-count" {
		method = statsv1.XrayStatsService_GetOnlineIPCount_FullMethodName
	}
	var input proto.Message = &statsv1.XrayOnlineRequest{Id: "primary", Name: name}
	if reference {
		method = "/service.NodeService/GetUserOnlineIpListStats"
		if scope == "online-count" {
			method = "/service.NodeService/GetUserOnlineStats"
		}
		input = &fixture.ReferenceTrafficRequest{Name: name}
	}
	return validateOnlineRead(scope, name, func(ctx context.Context, response proto.Message) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
		return fixture.CallError(conn.Invoke(ctx, method, input, response, grpc.StaticMethod()))
	})
}

// Both implementations return the account and the equivalent count or IP map.
// The reference's protobuf field numbers match these response projections.
func validateOnlineRead(scope, name string, read func(context.Context, proto.Message) error) fixture.ReadCall {
	if scope == "online-count" {
		return func(ctx context.Context) (uint32, error) {
			var response statsv1.XrayOnlineCount
			if err := read(ctx, &response); err != nil {
				return 0, err
			}
			if response.Name != name || response.Count != 1 {
				return 0, fmt.Errorf("expected one online IP, received %v", &response)
			}
			return 1, nil
		}
	}
	return func(ctx context.Context) (uint32, error) {
		var response statsv1.XrayOnlineIPs
		if err := read(ctx, &response); err != nil {
			return 0, err
		}
		if response.Name != name || len(response.LastSeenUnixSeconds) != 1 || response.LastSeenUnixSeconds[onlineSource] <= 0 {
			return 0, fmt.Errorf("expected an online IP with a timestamp, received %v", &response)
		}
		return 1, nil
	}
}
