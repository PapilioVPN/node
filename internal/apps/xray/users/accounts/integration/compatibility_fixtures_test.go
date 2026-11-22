//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/url"
	"testing"
	"time"

	v1 "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/grpcapi/proto/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func compatibilityData(t *testing.T, c clients, transport, kind, tag, selector string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if transport == "grpc" {
		client := v1.NewAccountsServiceClient(c.grpc)
		options := protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}
		if kind == "metadata" {
			r, err := client.GetAccountMetadata(ctx, &v1.GetAccountRequest{InstanceId: "edge-a", InboundTag: tag, Email: selector})
			if err != nil {
				t.Fatal(err)
			}
			data, err := options.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
		r, err := client.CheckAccountCompatibility(ctx, &v1.CheckAccountCompatibilityRequest{InstanceId: "edge-a", InboundTag: tag, AccountType: &selector})
		if err != nil {
			t.Fatal(err)
		}
		data, err := options.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	field, path, suffix := "account_type", "/accounts/compatibility", "compatibility.v1.get"
	if kind == "metadata" {
		field, path, suffix = "email", "/account/metadata", "metadata.v1.get"
	}
	if transport == "http" {
		r, err := c.http.Get(c.address + "/v1/xray/users/instances/edge-a" + path + "?inbound_tag=" + url.QueryEscape(tag) + "&" + field + "=" + url.QueryEscape(selector))
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != 200 {
			t.Fatal(string(data), err, r.StatusCode)
		}
		return data
	}
	body, _ := json.Marshal(map[string]string{"instance_id": "edge-a", "inbound_tag": tag, field: selector})
	r, err := c.nats.RequestWithContext(ctx, "fixture.accounts.xray.users.accounts."+suffix, body)
	if err != nil {
		t.Fatal(err)
	}
	return r.Data
}
func assertFamilyCompatibility(t *testing.T, c clients, transport, accountType string) {
	t.Helper()
	data := compatibilityData(t, c, transport, "compatibility", "members", accountType)
	var result struct {
		Compatible *bool  `json:"compatible"`
		Live       bool   `json:"live_accounts"`
		Expected   string `json:"expected_account_type"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Compatible == nil || !*result.Compatible || !result.Live || result.Expected != accountType {
		t.Fatal("native compatibility", string(data), err)
	}
	data = compatibilityData(t, c, transport, "metadata", "members", "original@example.test")
	var metadata struct {
		User struct {
			Email       string `json:"email"`
			AccountType string `json:"account_type"`
		} `json:"user"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.User.AccountType != accountType || bytes.Contains(data, []byte("configuration")) {
		t.Fatal("unsafe or mismatched summary", string(data), err)
	}
	page := accountView(t, c, transport, "metadata", "members", "")
	if page.Error != "" || len(page.Users) != 1 || page.Users[0].AccountType != accountType {
		t.Fatal("family list", page)
	}
}
