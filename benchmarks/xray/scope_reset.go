package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	statsv1 "github.com/PapilioVPN/node/internal/engines/xray/stats/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func scopeResetHTTP(client *http.Client, address string, accounts int, reference bool) trafficCall {
	if reference {
		payload, err := proto.Marshal(&ReferenceTrafficRequest{Type: 4, Reset_: true})
		check(err)
		return func(ctx context.Context) (uint32, error) {
			body, err := trafficHTTPRead(ctx, client, "https://"+address+"/stats/", payload)
			if err != nil {
				return 0, err
			}
			var reply ReferenceTrafficResponse
			if err := proto.Unmarshal(body, &reply); err != nil {
				return 0, err
			}
			return checkReferenceScope(accounts, reply.GetStats())
		}
	}

	endpoint := "https://" + address + "/v1/xray/instances/primary/stats/traffic/user/scope-resets?max_counters=" + strconv.Itoa(2*accounts)
	return func(ctx context.Context) (uint32, error) {
		body, err := trafficHTTPRequest(ctx, client, http.MethodPost, endpoint, nil)
		if err != nil {
			return 0, err
		}
		var reply struct {
			Outcome  string
			Counters []struct {
				Name, Direction string
				Result          struct {
					Effect   string
					Previous *int64 `json:"previous_bytes"`
				}
			}
		}
		if err := json.Unmarshal(body, &reply); err != nil {
			return 0, err
		}
		if reply.Outcome != "complete" || len(reply.Counters) != 2*accounts {
			return 0, fmt.Errorf("unexpected HTTP scope reset %q with %d results", reply.Outcome, len(reply.Counters))
		}
		validation := newScopeValidation(accounts)
		for _, row := range reply.Counters {
			if row.Result.Effect != "acknowledged" || row.Result.Previous == nil {
				return 0, fmt.Errorf("HTTP reset for %q did not acknowledge a value", row.Name)
			}
			if err := validation.add(row.Name, row.Direction, *row.Result.Previous); err != nil {
				return 0, err
			}
		}
		return validation.result()
	}
}

func nodeScopeResetGRPC(client statsv1.XrayStatsServiceClient, accounts int) trafficCall {
	request := &statsv1.XrayTrafficScopeResetRequest{
		Id: "primary", Scope: statsv1.XrayTrafficScope_XRAY_TRAFFIC_SCOPE_USER, MaxCounters: uint32(2 * accounts),
	}
	return func(ctx context.Context) (uint32, error) {
		reply, err := client.ResetTrafficScope(metadata.AppendToOutgoingContext(ctx, "x-api-key", key), request)
		if err != nil {
			return 0, trafficCallError(err)
		}
		if reply.GetOutcome() != statsv1.XrayTrafficScopeResetResult_COMPLETE || len(reply.GetCounters()) != 2*accounts {
			return 0, fmt.Errorf("unexpected gRPC scope reset %v with %d results", reply.GetOutcome(), len(reply.GetCounters()))
		}
		validation := newScopeValidation(accounts)
		for _, row := range reply.GetCounters() {
			direction := ""
			switch row.GetDirection() {
			case statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_UPLINK:
				direction = "uplink"
			case statsv1.XrayTrafficDirection_XRAY_TRAFFIC_DIRECTION_DOWNLINK:
				direction = "downlink"
			}
			if row.GetResult().GetEffect() != statsv1.XrayTrafficDirectionReset_ACKNOWLEDGED || row.GetResult().PreviousBytes == nil {
				return 0, fmt.Errorf("gRPC reset for %q did not acknowledge a value", row.GetName())
			}
			if err := validation.add(row.GetName(), direction, row.GetResult().GetPreviousBytes()); err != nil {
				return 0, err
			}
		}
		return validation.result()
	}
}

func referenceScopeResetGRPC(conn *grpc.ClientConn, accounts int) trafficCall {
	request := &ReferenceTrafficRequest{Type: 4, Reset_: true}
	return func(ctx context.Context) (uint32, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
		var reply ReferenceTrafficResponse
		if err := conn.Invoke(ctx, "/service.NodeService/GetStats", request, &reply, grpc.StaticMethod()); err != nil {
			return 0, trafficCallError(err)
		}
		return checkReferenceScope(accounts, reply.GetStats())
	}
}

func checkReferenceScope(accounts int, counters []*ReferenceTrafficStat) (uint32, error) {
	if len(counters) != 2*accounts {
		return 0, fmt.Errorf("reference scoped reset returned %d counters", len(counters))
	}
	validation := newScopeValidation(accounts)
	for _, row := range counters {
		if row.GetLink() != "traffic" {
			return 0, fmt.Errorf("unexpected reference counter kind: %v", row)
		}
		if err := validation.add(row.GetName(), row.GetType(), row.GetValue()); err != nil {
			return 0, err
		}
	}
	return validation.result()
}

type scopeValidation struct {
	directions []uint8
	nonzero    int
}

func newScopeValidation(accounts int) scopeValidation {
	return scopeValidation{directions: make([]uint8, accounts)}
}

func (v *scopeValidation) add(name, direction string, previous int64) error {
	suffix, ok := strings.CutPrefix(name, "account-")
	index, err := strconv.Atoi(suffix)
	if !ok || err != nil || index < 0 || index >= len(v.directions) || previous < 0 {
		return fmt.Errorf("unexpected fixture counter %q with %d previous bytes", name, previous)
	}
	var mask uint8
	switch direction {
	case "uplink":
		mask = 1
	case "downlink":
		mask = 2
	default:
		return fmt.Errorf("unexpected counter direction %q", direction)
	}
	if v.directions[index]&mask != 0 {
		return fmt.Errorf("duplicate fixture counter %q %s", name, direction)
	}
	v.directions[index] |= mask
	if previous > 0 {
		v.nonzero++
	}
	return nil
}

func (v *scopeValidation) result() (uint32, error) {
	if v.nonzero == 0 {
		return 0, nil
	}
	if v.nonzero == 2*len(v.directions) {
		return 1, nil
	}
	return 0, fmt.Errorf("scope reset returned %d nonzero counters out of %d", v.nonzero, 2*len(v.directions))
}
