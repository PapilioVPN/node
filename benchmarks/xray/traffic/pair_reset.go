package traffic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func nodePairHTTP(client *http.Client, address, name string) fixture.ReadCall {
	endpoint := "https://" + address + "/v1/xray/instances/primary/stats/traffic/user/pair-resets?name=" + url.QueryEscape(name)
	return func(ctx context.Context) (uint32, error) {
		body, err := fixture.HTTPRequest(ctx, client, http.MethodPost, endpoint, nil)
		if err != nil {
			return 0, err
		}
		type direction struct {
			Effect   string `json:"effect"`
			Previous *int64 `json:"previous_bytes"`
		}
		var reply struct{ Uplink, Downlink direction }
		if err := json.Unmarshal(body, &reply); err != nil {
			return 0, err
		}
		if reply.Uplink.Effect != "acknowledged" || reply.Downlink.Effect != "acknowledged" {
			return 0, fmt.Errorf("unexpected pair reset effects: %q and %q", reply.Uplink.Effect, reply.Downlink.Effect)
		}
		return pairValues(reply.Uplink.Previous, reply.Downlink.Previous)
	}
}

func nodePairGRPC(client statsv1.XrayStatsServiceClient, name string) fixture.ReadCall {
	request := &statsv1.XrayTrafficPairResetRequest{Id: "primary", Scope: statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, Name: name}
	return func(ctx context.Context) (uint32, error) {
		reply, err := client.ResetTrafficPair(metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key), request)
		if err != nil {
			return 0, fixture.CallError(err)
		}
		up, down := reply.GetUplink(), reply.GetDownlink()
		if up.GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED ||
			down.GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED {
			return 0, fmt.Errorf("unexpected pair reset effects: %v", reply)
		}
		return pairValues(up.PreviousBytes, down.PreviousBytes)
	}
}

func referencePairHTTP(client *http.Client, address, name string) fixture.ReadCall {
	payload, err := proto.Marshal(&fixture.ReferenceTrafficRequest{Name: name, Type: 5, Reset_: true})
	fixture.Check(err)
	return func(ctx context.Context) (uint32, error) {
		body, err := fixture.HTTPRead(ctx, client, "https://"+address+"/stats/", payload)
		if err != nil {
			return 0, err
		}
		var reply fixture.ReferenceTrafficResponse
		if err := proto.Unmarshal(body, &reply); err != nil {
			return 0, err
		}
		return referencePairValues(name, reply.GetStats())
	}
}

func referencePairGRPC(conn *grpc.ClientConn, name string) fixture.ReadCall {
	request := &fixture.ReferenceTrafficRequest{Name: name, Type: 5, Reset_: true}
	return func(ctx context.Context) (uint32, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
		var reply fixture.ReferenceTrafficResponse
		if err := conn.Invoke(ctx, "/service.NodeService/GetStats", request, &reply, grpc.StaticMethod()); err != nil {
			return 0, fixture.CallError(err)
		}
		return referencePairValues(name, reply.GetStats())
	}
}

func referencePairValues(name string, rows []*fixture.ReferenceTrafficStat) (uint32, error) {
	if len(rows) != 2 {
		return 0, fmt.Errorf("reference pair returned %d counters", len(rows))
	}
	var up, down *int64
	for _, row := range rows {
		if row.GetName() != name || row.GetLink() != "traffic" {
			return 0, fmt.Errorf("unexpected reference reset counter: %v", row)
		}
		switch row.GetType() {
		case "uplink":
			up = &row.Value
		case "downlink":
			down = &row.Value
		}
	}
	return pairValues(up, down)
}

func pairValues(up, down *int64) (uint32, error) {
	if up == nil || down == nil {
		return 0, fmt.Errorf("reset did not return both directional byte counts")
	}
	if *up == 0 && *down == 0 {
		return 0, nil
	}
	if *up > 0 && *down > 0 {
		return 1, nil
	}
	return 0, fmt.Errorf("unexpected fixture reset values: %d and %d", *up, *down)
}
