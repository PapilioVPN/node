package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const syncChunkSize = 50

func syncChunkHTTP(client *http.Client, addr string) syncAccounts {
	return func(ctx context.Context, accounts []syncAccount) error {
		var body bytes.Buffer
		encoder := json.NewEncoder(&body)
		for index, batch := range chunkBatches(accounts) {
			frame := struct {
				Index    int           `json:"index"`
				Last     bool          `json:"last"`
				Accounts []syncAccount `json:"accounts"`
			}{index, index == (len(accounts)-1)/syncChunkSize, batch}
			if err := encoder.Encode(frame); err != nil {
				return err
			}
		}
		wire, err := accountHTTPCall(ctx, client, http.MethodPut, "https://"+addr+accountPath+"/membership/stream", body.Bytes(), http.StatusOK)
		if err != nil {
			return err
		}
		return syncHTTPResult(wire)
	}
}

func referenceChunkHTTP(client *http.Client, addr string) syncAccounts {
	return func(ctx context.Context, accounts []syncAccount) error {
		var body []byte
		for index, batch := range chunkBatches(accounts) {
			wire, err := proto.Marshal(referenceChunk(accounts, index, batch))
			if err != nil {
				return err
			}
			body = binary.AppendUvarint(body, uint64(len(wire)))
			body = append(body, wire...)
		}
		_, err := accountHTTPCall(ctx, client, http.MethodPut, "https://"+addr+"/users/sync/chunked", body, http.StatusOK)
		return err
	}
}

func syncChunkGRPC(conn *grpc.ClientConn) syncAccounts {
	client := accountsv1.NewXrayAccountsServiceClient(conn)
	return func(ctx context.Context, accounts []syncAccount) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		stream, err := client.SyncUsersChunked(syncAuth(ctx))
		if err != nil {
			return err
		}
		for index, batch := range chunkBatches(accounts) {
			err := stream.Send(&accountsv1.UsersChunk{
				Id: "primary", InboundTag: "vless", Index: uint32(index),
				Last: index == (len(accounts)-1)/syncChunkSize, Accounts: syncWireAccounts(batch),
			})
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
		result, err := stream.CloseAndRecv()
		return checkedSync(result.GetOutcome(), err)
	}
}

func referenceChunkGRPC(conn *grpc.ClientConn) syncAccounts {
	return func(ctx context.Context, accounts []syncAccount) error {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		desc := &grpc.StreamDesc{ClientStreams: true}
		stream, err := conn.NewStream(syncAuth(ctx), desc, "/service.NodeService/SyncUsersChunked", grpc.StaticMethod())
		if err != nil {
			return err
		}
		wrapped := grpc.GenericClientStream[ReferenceAccountChunk, emptypb.Empty]{ClientStream: stream}
		for index, batch := range chunkBatches(accounts) {
			err := wrapped.Send(referenceChunk(accounts, index, batch))
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
		}
		_, err = wrapped.CloseAndRecv()
		return err
	}
}

func referenceChunk(all []syncAccount, index int, batch []syncAccount) *ReferenceAccountChunk {
	return &ReferenceAccountChunk{Users: referenceUsers(batch), Index: uint64(index),
		Last: index == (len(all)-1)/syncChunkSize}
}

func chunkBatches(accounts []syncAccount) [][]syncAccount {
	return slices.Collect(slices.Chunk(accounts, syncChunkSize))
}

func syncChunkCases(prepare syncAccounts) []syncCase {
	var cases []syncCase
	for _, size := range []int{100, 1000} {
		sentinel := fixtureAccount(10000)
		desired, old := []syncAccount{sentinel}, []syncAccount{sentinel}
		var payload []syncAccount
		for i := range size / 2 {
			add, remove := fixtureAccount(i), fixtureAccount(i+size)
			desired = append(desired, add)
			old = append(old, remove)
			payload = append(payload, add, syncAccount{Email: remove.Email})
		}
		cases = append(cases,
			syncCase{name: "converged", payload: payload, initial: desired, final: desired, prepare: prepare},
			syncCase{name: "behind-delta", payload: payload, initial: old, final: desired, prepare: prepare},
		)
	}
	return cases
}
