package integration_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	statsv1 "github.com/PapilioVPN/node/internal/engines/wireguard/stats/bridge/grpc/proto/v1"
)

func assertPeerActivity(t *testing.T, f fixture, client statsv1.WireGuardStatsServiceClient, key []byte, active bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request := &statsv1.WireGuardPeerTrafficRequest{Name: interfaceName, PublicKey: key}
	state, err := client.GetPeerActivity(ctx, request)
	if err != nil || state.GetActive() != active || (state.GetLastActiveAt() != nil) != active {
		t.Fatalf("peer activity = %v, %v", state, err)
	}
	endpoints, err := client.ListPeerEndpointIPs(ctx, request)
	if err != nil || (len(endpoints.GetEndpoints()) != 0) != active {
		t.Fatalf("peer endpoints = %v, %v", endpoints, err)
	}
	if active && (len(endpoints.GetEndpoints()) != 1 || endpoints.GetEndpoints()[0].GetIp() != "198.51.100.2") {
		t.Fatalf("peer endpoints = %v, want tunnel underlay IP", endpoints)
	}
	path := f.address + interfaceURL + "/peers/" + base64.RawURLEncoding.EncodeToString(key)
	for _, suffix := range []string{"/activity", "/endpoint-ips"} {
		resp, err := f.http.Get(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Active    bool
			Endpoints []struct{ IP string }
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()
		if resp.StatusCode != 200 || err != nil || suffix == "/activity" && result.Active != active || suffix == "/endpoint-ips" && (len(result.Endpoints) != 0) != active {
			t.Fatalf("HTTP %s = %+v, %v", suffix, result, err)
		}
		if suffix == "/endpoint-ips" && active && result.Endpoints[0].IP != "198.51.100.2" {
			t.Fatalf("HTTP endpoint = %+v", result.Endpoints)
		}
	}
}
