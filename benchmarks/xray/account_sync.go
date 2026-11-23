package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/membership"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type syncAccount struct {
	Email string `json:"email"`
	UUID  string `json:"uuid,omitempty"`
}

type syncAccounts func(context.Context, []syncAccount) error

func syncUserHTTP(client accountHTTPClient, node string) syncAccounts {
	return func(ctx context.Context, accounts []syncAccount) error {
		return client(ctx, node+accountPath+"/membership", accounts[0])
	}
}

type accountHTTPClient func(context.Context, string, any) error

func syncUserGRPC(conn *grpc.ClientConn) syncAccounts {
	client := accountsv1.NewXrayAccountsServiceClient(conn)
	return func(ctx context.Context, accounts []syncAccount) error {
		account := accounts[0]
		req := &accountsv1.SyncUserRequest{Id: "primary", InboundTag: "vless", Email: account.Email}
		if account.UUID != "" {
			req.Uuid = &account.UUID
		}
		result, err := client.SyncUser(syncAuth(ctx), req)
		return checkedSync(result.GetOutcome(), err)
	}
}

func syncAuth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
}

func checkedSync(outcome string, err error) error {
	if err != nil {
		return err
	}
	if outcome != "confirmed" {
		return errors.New("sync request did not confirm")
	}
	return nil
}

type syncCase struct {
	name                    string
	payload, initial, final []syncAccount
	prepare                 syncAccounts
}

func syncUserCases(prepare syncAccounts) []syncCase {
	account := fixtureAccount(0)
	present := []syncAccount{account}
	absent := []syncAccount{{Email: account.Email}}
	return []syncCase{
		{name: "converged", payload: present, initial: present, final: present, prepare: prepare},
		{name: "behind-add", payload: present, initial: absent, final: present, prepare: prepare},
		{name: "behind-remove", payload: absent, initial: present, final: absent, prepare: prepare},
	}
}

func fixtureAccount(index int) syncAccount {
	return syncAccount{Email: fmt.Sprintf("fixture-%05d@example.invalid", index),
		UUID: fmt.Sprintf("7ccb2c70-6a53-4a6a-85e5-%012x", index+1)}
}

func benchmarkAccountSync(protocol string, targets []target, rounds, samples int, workload accountWorkload) {
	rawFile, err := os.Create(workload.samples)
	check(err)
	defer rawFile.Close()
	raw := csv.NewWriter(rawFile)
	summary := csv.NewWriter(os.Stdout)
	headers := []string{"transport", "round", "target", "case", "accounts"}
	check(raw.Write(append(slices.Clone(headers), "sample", "duration_ns", "success")))
	check(summary.Write(append(slices.Clone(headers), "attempts", "success", "success_rate", "completion_seconds", "p50_us", "p95_us")))

	for round := range rounds {
		order := []int{0, 1}
		if round%2 == 1 {
			slices.Reverse(order)
		}
		for _, index := range order {
			t := targets[index]
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			check(t.call(ctx, start))
			call, cases := t.accounts.syncUser, syncUserCases(t.accounts.syncUser)
			switch workload.sync {
			case "users":
				call, cases = t.accounts.syncUsers, syncUsersCases(t.accounts.syncUsers)
			case "chunked":
				call, cases = t.accounts.syncChunk, syncChunkCases(t.accounts.syncUsers)
			}
			for _, scenario := range cases {
				runSyncCase(ctx, index, workload, call, scenario, samples,
					[]string{protocol, strconv.Itoa(round + 1), t.name, scenario.name, strconv.Itoa(len(scenario.payload))},
					raw, summary)
			}
			check(t.call(ctx, stop))
			cancel()
			raw.Flush()
			summary.Flush()
			check(raw.Error())
			check(summary.Error())
		}
	}
}

func runSyncCase(ctx context.Context, index int, workload accountWorkload, call syncAccounts, scenario syncCase, samples int, fields []string, raw, summary *csv.Writer) {
	verify := func(want []syncAccount) bool {
		conn := accountNativeConn(index, workload)
		defer conn.Close()
		return verifyMembership(ctx, conn, want)
	}
	check(scenario.prepare(ctx, scenario.initial))
	if !verify(scenario.initial) {
		check(errors.New("benchmark starting membership did not match fixture"))
	}
	check(call(ctx, scenario.payload))
	if !verify(scenario.final) {
		check(errors.New("benchmark warm-up membership did not match fixture"))
	}

	latencies := make([]time.Duration, 0, samples)
	var total time.Duration
	for sample := range samples {
		if !slices.Equal(scenario.initial, scenario.final) {
			check(scenario.prepare(ctx, scenario.initial))
		}
		request, cancel := context.WithTimeout(ctx, 15*time.Second)
		started := time.Now()
		err := call(request, scenario.payload)
		elapsed := time.Since(started)
		cancel()
		total += elapsed
		membershipOK := verify(scenario.final)
		success := err == nil && membershipOK
		if success {
			latencies = append(latencies, elapsed)
		}
		check(raw.Write(append(slices.Clone(fields), strconv.Itoa(sample+1), strconv.FormatInt(elapsed.Nanoseconds(), 10), strconv.FormatBool(success))))
	}
	slices.Sort(latencies)
	p50, p95 := "", ""
	if len(latencies) > 0 {
		p50, p95 = percentile(latencies, 50), percentile(latencies, 95)
	}
	check(summary.Write(append(slices.Clone(fields), strconv.Itoa(samples), strconv.Itoa(len(latencies)),
		fmt.Sprintf("%.6f", float64(len(latencies))/float64(samples)), fmt.Sprintf("%.9f", total.Seconds()), p50, p95)))
}

func verifyMembership(ctx context.Context, conn *grpc.ClientConn, want []syncAccount) bool {
	expected := make(map[string]membership.IDKey, len(want))
	for _, account := range want {
		if account.UUID != "" {
			id, _ := membership.ParseUUID(account.UUID)
			expected[account.Email] = id.Key()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var users native.VlessInbound // Same repeated User field as GetInboundUserResponse.
	err := conn.Invoke(ctx, native.HandlerService+"GetInboundUsers", &native.GetInboundUserRequest{Tag: "vless"}, &users)
	if err != nil || len(users.Users) != len(expected) {
		return false
	}
	for _, user := range users.Users {
		key, exists := expected[user.GetEmail()]
		var account native.VlessAccount
		if !exists || proto.Unmarshal(user.GetAccount().GetValue(), &account) != nil {
			return false
		}
		id, valid := membership.ParseUUID(account.GetId())
		if !valid || id.Key() != key {
			return false
		}
		delete(expected, user.GetEmail())
	}
	return len(expected) == 0
}

func syncHTTPResult(wire []byte) error {
	var result struct{ Outcome string }
	err := json.Unmarshal(wire, &result)
	return checkedSync(result.Outcome, err)
}

func syncUsersHTTP(client accountHTTPClient, node string) syncAccounts {
	return func(ctx context.Context, accounts []syncAccount) error {
		return client(ctx, node+accountPath, struct {
			Accounts []syncAccount `json:"accounts"`
		}{accounts})
	}
}

func syncUsersGRPC(conn *grpc.ClientConn) syncAccounts {
	client := accountsv1.NewXrayAccountsServiceClient(conn)
	return func(ctx context.Context, accounts []syncAccount) error {
		result, err := client.SyncUsers(syncAuth(ctx), &accountsv1.SyncUsersRequest{
			Id: "primary", InboundTag: "vless",
			Snapshot: &accountsv1.VLESSAccounts{Accounts: syncWireAccounts(accounts)},
		})
		return checkedSync(result.GetOutcome(), err)
	}
}

func syncWireAccounts(accounts []syncAccount) []*accountsv1.VLESSAccount {
	wire := make([]*accountsv1.VLESSAccount, len(accounts))
	for i := range accounts {
		wire[i] = &accountsv1.VLESSAccount{Email: accounts[i].Email}
		if accounts[i].UUID != "" {
			wire[i].Uuid = &accounts[i].UUID
		}
	}
	return wire
}

func referenceUsers(accounts []syncAccount) []*ReferenceAccountUser {
	users := make([]*ReferenceAccountUser, len(accounts))
	for i, account := range accounts {
		users[i] = referenceAccount(account.Email, account.UUID)
	}
	return users
}

func syncUsersCases(prepare syncAccounts) []syncCase {
	var cases []syncCase
	for _, size := range []int{100, 1000} {
		desired, old := make([]syncAccount, size), make([]syncAccount, size)
		for i := range size {
			desired[i], old[i] = fixtureAccount(i), fixtureAccount(i+size)
		}
		cases = append(cases,
			syncCase{name: "converged", payload: desired, initial: desired, final: desired, prepare: prepare},
			syncCase{name: "behind-replace", payload: desired, initial: old, final: desired, prepare: prepare},
		)
	}
	return cases
}
