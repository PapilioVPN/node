package instances

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

var operations = [3]string{"start", "status", "stop"}

func HTTP(client *http.Client, node, reference string, payload []byte, methods protoreflect.MethodDescriptors) (nodeCall, referenceCall fixture.LifecycleCall) {
	nodeCall = httpCall(client, node, [3]string{"PUT", "GET", "DELETE"},
		[3]string{"/v1/xray/instances/primary/execution", "/v1/xray/instances/primary", "/v1/xray/instances/primary/execution"},
		nil, verifyNodeJSON)
	referenceCall = httpCall(client, reference, [3]string{"POST", "GET", "PUT"},
		[3]string{"/start", "/info", "/stop"}, payload, func(op fixture.Action, data []byte) error {
			return verifyReference(op, methods.ByName("GetBaseInfo").Output(), data)
		})
	return
}

func GRPC(nodeConn, refConn *grpc.ClientConn, refStart *dynamicpb.Message, methods protoreflect.MethodDescriptors) (nodeCall, referenceCall fixture.LifecycleCall) {
	nodeClient := instancesv1.NewXrayInstancesServiceClient(nodeConn)
	nodeCall = func(ctx context.Context, op fixture.Action) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
		calls := [...]func(context.Context, *instancesv1.XrayInstanceRequest, ...grpc.CallOption) (*instancesv1.XrayInstanceStatus, error){
			nodeClient.Start, nodeClient.GetStatus, nodeClient.Stop,
		}
		resp, err := calls[op](ctx, &instancesv1.XrayInstanceRequest{Id: "primary"})
		if err != nil {
			return err
		}
		want := instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY
		if op == fixture.Stop {
			want = instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_STOPPED
		}
		if resp.GetId() != "primary" || resp.GetState() != want {
			return fmt.Errorf("unexpected PapilioVPN status %v", resp)
		}
		return nil
	}
	referenceCall = func(ctx context.Context, op fixture.Action) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
		method := methods.ByName([3]protoreflect.Name{"Start", "GetBaseInfo", "Stop"}[op])
		req := dynamicpb.NewMessage(method.Input())
		if op == fixture.Start {
			req = refStart
		}
		resp := dynamicpb.NewMessage(method.Output())
		if err := refConn.Invoke(ctx, "/service.NodeService/"+string(method.Name()), req, resp); err != nil {
			return err
		}
		if op != fixture.Stop && !resp.Get(resp.Descriptor().Fields().ByName("started")).Bool() {
			return errors.New("PasarGuard reported an inactive Xray process")
		}
		return nil
	}
	return
}

func Benchmark(protocol string, targets [2]fixture.Instance, rounds, cycles, reads int) {
	output := csv.NewWriter(os.Stdout)
	fixture.Check(output.Write([]string{"transport", "round", "node", "operation", "samples", "p50_us", "p95_us", "p99_us"}))
	for round := range rounds {
		order := []int{0, 1}
		if round%2 == 1 {
			slices.Reverse(order)
		}
		for _, index := range order {
			t := targets[index]
			exercise(t, 1, nil)
			samples := [3][]time.Duration{
				make([]time.Duration, 0, cycles), make([]time.Duration, 0, cycles*reads), make([]time.Duration, 0, cycles),
			}
			for range cycles {
				exercise(t, reads, &samples)
			}
			for i, timings := range samples {
				slices.Sort(timings)
				fixture.Check(output.Write([]string{protocol, strconv.Itoa(round + 1), t.Name, operations[i], strconv.Itoa(len(timings)),
					fixture.Percentile(timings, 50), fixture.Percentile(timings, 95), fixture.Percentile(timings, 99)}))
			}
			output.Flush()
			fixture.Check(output.Error())
		}
	}
}
func exercise(t fixture.Instance, reads int, samples *[3][]time.Duration) {
	call := func(op fixture.Action) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		started := time.Now()
		err := t.Call(ctx, op)
		elapsed := time.Since(started)
		cancel()
		if err != nil {
			log.Fatalf("%s %s: %v", t.Name, operations[op], err)
		}
		if samples != nil {
			samples[op] = append(samples[op], elapsed)
		}
	}
	call(fixture.Start)
	if !fixture.HasChild(t.PID) {
		log.Fatalf("%s start returned without a child", t.Name)
	}
	for range reads {
		call(fixture.Status)
	}
	call(fixture.Stop)
	if fixture.HasChild(t.PID) {
		log.Fatalf("%s stop returned before its child was reaped", t.Name)
	}
}

func httpCall(client *http.Client, address string, methods, paths [3]string, payload []byte, verify func(fixture.Action, []byte) error) func(context.Context, fixture.Action) error {
	return func(ctx context.Context, op fixture.Action) error {
		var body io.Reader
		if op == fixture.Start && payload != nil {
			body = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, methods[op], "https://"+address+paths[op], body)
		if err != nil {
			return err
		}
		req.Header.Set("X-API-Key", fixture.Key)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("%s: %s", resp.Status, data)
		}
		return verify(op, data)
	}
}

func verifyNodeJSON(op fixture.Action, data []byte) error {
	var response struct{ ID, State string }
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	want := "ready"
	if op == fixture.Stop {
		want = "stopped"
	}
	if response.ID != "primary" || response.State != want {
		return fmt.Errorf("unexpected PapilioVPN status %s", data)
	}
	return nil
}

func verifyReference(op fixture.Action, descriptor protoreflect.MessageDescriptor, data []byte) error {
	if op == fixture.Stop {
		return nil
	}
	response := dynamicpb.NewMessage(descriptor)
	if err := proto.Unmarshal(data, response); err != nil {
		return err
	}
	if !response.Get(descriptor.Fields().ByName("started")).Bool() {
		return errors.New("PasarGuard reported an inactive Xray process")
	}
	return nil
}
