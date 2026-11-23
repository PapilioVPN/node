package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"github.com/PapilioVPN/node/internal/engines/xray/accounts/native"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const accountPath = "/v1/xray/instances/primary/inbounds/vless/accounts"

type accountWorkload struct {
	runtimeDir, generated, samples string
	mutations                      int
	sync                           bool
}

type accountCalls struct {
	syncUser    syncAccounts
	add, remove func(context.Context, string, string) error
	count       func(context.Context) (uint32, error)
}

func accountHTTP(client *http.Client, node, reference string) (accountCalls, accountCalls) {
	type body struct {
		Email string `json:"email"`
		UUID  string `json:"uuid,omitempty"`
	}
	mutate := func(ctx context.Context, method string, input body) error {
		wire, err := json.Marshal(input)
		if err != nil {
			return err
		}
		_, err = accountHTTPCall(ctx, client, method, "https://"+node+accountPath, wire, http.StatusNoContent)
		return err
	}
	syncUser := func(ctx context.Context, email, uuid string) error {
		wire, err := proto.Marshal(referenceAccount(email, uuid))
		if err != nil {
			return err
		}
		_, err = accountHTTPCall(ctx, client, http.MethodPut, "https://"+reference+"/user/sync", wire, http.StatusOK)
		return err
	}
	syncNode := func(ctx context.Context, endpoint string, input any) error {
		wire, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body, err := accountHTTPCall(ctx, client, http.MethodPut, "https://"+endpoint, wire, http.StatusOK)
		if err != nil {
			return err
		}
		return syncHTTPResult(body)
	}
	syncRef := func(ctx context.Context, accounts []syncAccount) error {
		return syncUser(ctx, accounts[0].Email, accounts[0].UUID)
	}
	return accountCalls{
		syncUser: syncUserHTTP(syncNode, node),
		add: func(ctx context.Context, email, uuid string) error {
			return mutate(ctx, http.MethodPost, body{Email: email, UUID: uuid})
		},
		remove: func(ctx context.Context, email, _ string) error {
			return mutate(ctx, http.MethodDelete, body{Email: email})
		},
		count: func(ctx context.Context) (uint32, error) {
			wire, err := accountHTTPCall(ctx, client, http.MethodGet, "https://"+node+accountPath+"/count", nil, http.StatusOK)
			if err != nil {
				return 0, err
			}
			var result struct{ Count uint32 }
			err = json.Unmarshal(wire, &result)
			return result.Count, err
		},
	}, accountCalls{syncUser: syncRef, add: syncUser, remove: func(ctx context.Context, email, _ string) error { return syncUser(ctx, email, "") }}
}

func accountHTTPCall(ctx context.Context, client *http.Client, method, endpoint string, body []byte, want int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		return nil, fmt.Errorf("account operation returned HTTP %s", resp.Status)
	}
	return io.ReadAll(resp.Body)
}

func accountGRPC(node, reference *grpc.ClientConn) (accountCalls, accountCalls) {
	client := accountsv1.NewXrayAccountsServiceClient(node)
	auth := func(ctx context.Context) context.Context {
		return metadata.AppendToOutgoingContext(ctx, "x-api-key", key)
	}
	syncUser := func(ctx context.Context, email, uuid string) error {
		desc := &grpc.StreamDesc{ClientStreams: true}
		stream, err := reference.NewStream(auth(ctx), desc, "/service.NodeService/SyncUser", grpc.StaticMethod())
		if err != nil {
			return err
		}
		wrapped := grpc.GenericClientStream[ReferenceAccountUser, emptypb.Empty]{ClientStream: stream}
		if err := wrapped.Send(referenceAccount(email, uuid)); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		_, err = wrapped.CloseAndRecv()
		return err
	}
	syncRef := func(ctx context.Context, accounts []syncAccount) error {
		return syncUser(ctx, accounts[0].Email, accounts[0].UUID)
	}
	return accountCalls{
		syncUser: syncUserGRPC(node),
		add: func(ctx context.Context, email, uuid string) error {
			_, err := client.AddVLESSAccount(auth(ctx), &accountsv1.AddVLESSAccountRequest{Id: "primary", InboundTag: "vless", Email: email, Uuid: uuid})
			return err
		},
		remove: func(ctx context.Context, email, _ string) error {
			_, err := client.RemoveVLESSAccount(auth(ctx), &accountsv1.RemoveVLESSAccountRequest{Id: "primary", InboundTag: "vless", Email: email})
			return err
		},
		count: func(ctx context.Context) (uint32, error) {
			resp, err := client.CountVLESSAccounts(auth(ctx), &accountsv1.CountVLESSAccountsRequest{Id: "primary", InboundTag: "vless"})
			return uint32(resp.GetCount()), err
		},
	}, accountCalls{syncUser: syncRef, add: syncUser, remove: func(ctx context.Context, email, _ string) error { return syncUser(ctx, email, "") }}
}

func referenceAccount(email, uuid string) *ReferenceAccountUser {
	request := &ReferenceAccountUser{Email: email}
	if uuid != "" {
		request.Proxies = &ReferenceAccountProxy{Vless: &ReferenceVLESS{Id: uuid}}
		request.Inbounds = []string{"vless"}
	}
	return request
}

func benchmarkAccounts(protocol string, targets []target, rounds int, clients []int, window time.Duration, workload accountWorkload) {
	summary := csv.NewWriter(os.Stdout)
	check(summary.Write([]string{"transport", "round", "target", "operation", "clients", "success", "seconds", "success_per_sec", "p50_us", "p95_us"}))
	var raw *csv.Writer
	if workload.samples != "" {
		file, err := os.Create(workload.samples)
		check(err)
		defer func() { check(file.Close()) }()
		compressed := gzip.NewWriter(file)
		defer func() { check(compressed.Close()) }()
		raw = csv.NewWriter(compressed)
		check(raw.Write([]string{"transport", "round", "target", "operation", "clients", "duration_ns"}))
	}
	record := func(round int, target, operation string, clients int, timings []time.Duration, elapsed time.Duration) {
		slices.Sort(timings)
		fields := []string{protocol, strconv.Itoa(round + 1), target, operation, strconv.Itoa(clients)}
		if raw != nil {
			for _, elapsed := range timings {
				check(raw.Write(append(fields, strconv.FormatInt(elapsed.Nanoseconds(), 10))))
			}
		}
		check(summary.Write(append(fields, strconv.Itoa(len(timings)), fmt.Sprintf("%.6f", elapsed.Seconds()),
			fmt.Sprintf("%.2f", float64(len(timings))/elapsed.Seconds()), percentile(timings, 50), percentile(timings, 95))))
	}
	identities := make([][2]string, workload.mutations)
	for i := range identities {
		identities[i] = [2]string{fmt.Sprintf("fixture-%05d@example.invalid", i), fmt.Sprintf("7ccb2c70-6a53-4a6a-85e5-%012x", i+1)}
	}
	for round := range rounds {
		order := []int{0, 1}
		if round%2 == 1 {
			slices.Reverse(order)
		}
		for _, index := range order {
			t := targets[index]
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second+time.Duration(len(clients))*window*2)
			check(t.call(ctx, start))
			conn := accountNativeConn(index, workload)
			direct := func(ctx context.Context) (uint32, error) {
				var result native.GetInboundUsersCountResponse
				err := conn.Invoke(ctx, native.CountUsersMethod, &native.GetInboundUserRequest{Tag: "vless"}, &result, grpc.StaticMethod())
				return uint32(result.Count), err
			}
			verifyCount(ctx, direct, 0)
			check(t.accounts.add(ctx, identities[0][0], identities[0][1]))
			verifyCount(ctx, direct, 1)
			check(t.accounts.remove(ctx, identities[0][0], identities[0][1]))
			verifyCount(ctx, direct, 0)

			run := func(op string, call func(context.Context, string, string) error) {
				timings := make([]time.Duration, len(identities))
				started := time.Now()
				for i, account := range identities {
					request := time.Now()
					check(call(ctx, account[0], account[1]))
					timings[i] = time.Since(request)
				}
				record(round, t.name, op, 1, timings, time.Since(started))
			}
			run("add", t.accounts.add)
			verifyCount(ctx, direct, uint32(len(identities)))
			if index == 0 {
				measure := []struct {
					name string
					call func(context.Context) (uint32, error)
				}{{t.name, t.accounts.count}, {"direct-xray", direct}}
				if round%2 == 1 {
					slices.Reverse(measure)
				}
				for _, target := range measure {
					for _, count := range clients {
						call := func(ctx context.Context) (uint32, error) {
							got, err := target.call(ctx)
							if err == nil && got != uint32(len(identities)) {
								err = fmt.Errorf("account count = %d, want %d", got, len(identities))
							}
							return got, err
						}
						result, err := pollRuntimeStats(ctx, call, count, window)
						check(err)
						if result.rejected != 0 {
							check(fmt.Errorf("%s rejected %d account count reads", target.name, result.rejected))
						}
						record(round, target.name, "count", count, result.timings, result.elapsed)
					}
				}
			}
			run("remove", t.accounts.remove)
			verifyCount(ctx, direct, 0)
			check(conn.Close())
			check(t.call(ctx, stop))
			cancel()
			summary.Flush()
			check(summary.Error())
			if raw != nil {
				raw.Flush()
				check(raw.Error())
			}
		}
	}
}

func verifyCount(ctx context.Context, direct func(context.Context) (uint32, error), want uint32) {
	count, err := direct(ctx)
	check(err)
	if count != want {
		check(fmt.Errorf("fixture Xray account count = %d, want %d", count, want))
	}
}

func accountNativeConn(index int, workload accountWorkload) *grpc.ClientConn {
	var address string
	if index == 0 {
		paths, err := filepath.Glob(filepath.Join(workload.runtimeDir, "node-xray-*", "api.sock"))
		check(err)
		if len(paths) != 1 {
			check(fmt.Errorf("found %d fixture native sockets, want one", len(paths)))
		}
		socket := url.URL{Scheme: "unix", Path: paths[0]}
		address = socket.String()
	} else {
		var config struct {
			Inbounds []struct {
				Tag  string
				Port int
			}
		}
		check(json.Unmarshal(read(workload.generated), &config))
		for _, inbound := range config.Inbounds {
			if inbound.Tag == "API_INBOUND" && inbound.Port > 0 {
				address = "127.0.0.1:" + strconv.Itoa(inbound.Port)
				break
			}
		}
		if address == "" {
			check(errors.New("missing PasarGuard fixture native inbound"))
		}
	}
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	check(err)
	return conn
}
