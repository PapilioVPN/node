package instances

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"
	accountsv1 "github.com/PapilioVPN/node/internal/engines/xray/accounts/bridge/grpc/proto/v1"
	instancesv1 "github.com/PapilioVPN/node/internal/engines/xray/instances/bridge/grpc/proto/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const startEmail = "1.fixture@example.invalid"

var startUUID = uuid.MustParse("d5120cd4-15b3-4ca7-a198-0a32d269c135")

type startPath struct {
	name   string
	port   int
	origin int
	action instancesv1.XrayStartAction
	seed   bool
	config []byte
}

func BenchmarkStartPaths(targets [2]fixture.Instance, node, reference *grpc.ClientConn,
	method protoreflect.MethodDescriptor, dir string, rounds, cycles int) {
	origins := [2]*httptest.Server{}
	for i := range origins {
		origins[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, i)
		}))
		defer origins[i].Close()
	}
	const originalPort, replacementPort = 24440, 24441
	paths := []startPath{
		{name: "start", port: originalPort, action: instancesv1.XrayStartAction_XRAY_START_ACTION_STARTED, seed: true},
		{name: "heartbeat", port: originalPort, action: instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT, seed: true},
		{name: "live-update", port: originalPort, origin: 1, action: instancesv1.XrayStartAction_XRAY_START_ACTION_LIVE_UPDATED, seed: true},
		{name: "restart", port: replacementPort, origin: 1, action: instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED, seed: true},
		{name: "restart-replay", port: originalPort, origin: 1, action: instancesv1.XrayStartAction_XRAY_START_ACTION_RESTARTED},
	}
	fixture.Check(os.MkdirAll(dir, 0755))
	writers := make([]*csv.Writer, len(paths))
	for i, path := range paths {
		paths[i].config = startConfiguration(path, origins)
		file, err := os.Create(filepath.Join(dir, path.name+".csv"))
		fixture.Check(err)
		defer func() { fixture.Check(file.Close()) }()
		writers[i] = csv.NewWriter(file)
		fixture.Check(writers[i].Write([]string{"transport", "round", "node", "operation", "sample", "duration_ns"}))
	}

	nodeClient := instancesv1.NewXrayInstancesServiceClient(node)
	calls := make([][2]func(context.Context) error, len(paths))
	for i, path := range paths {
		calls[i][0] = nodeStartPath(nodeClient, path)
		if path.seed {
			calls[i][1] = referenceStartPath(reference, method, path)
		}
	}
	for round := 1; round <= rounds; round++ {
		for offset := range targets {
			index := (offset + round - 1) % len(targets)
			target := targets[index]
			for sample := 0; sample <= cycles; sample++ {
				var previousPID string
				for i, path := range paths {
					call := calls[i][index]
					if call == nil {
						continue
					}
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
					begin := time.Now()
					err := call(ctx)
					elapsed := time.Since(begin)
					cancel()
					if err != nil {
						log.Fatalf("%s %s: %v", target.Name, path.name, err)
					}
					children := fixture.ChildPIDs(target.PID)
					if len(children) != 1 {
						log.Fatalf("%s %s: expected one Xray child, got %v", target.Name, path.name, children)
					}
					pid := children[0]
					preserve := path.action == instancesv1.XrayStartAction_XRAY_START_ACTION_HEARTBEAT ||
						path.action == instancesv1.XrayStartAction_XRAY_START_ACTION_LIVE_UPDATED
					if (pid == previousPID) != preserve {
						log.Fatalf("%s %s: Xray PID changed from %q to %q", target.Name, path.name, previousPID, pid)
					}
					previousPID = pid
					if err := verifyStartTraffic(path, origins[0].Listener.Addr().(*net.TCPAddr).Port); err != nil {
						log.Fatalf("%s %s: %v", target.Name, path.name, err)
					}
					if sample > 0 {
						fixture.Check(writers[i].Write([]string{"grpc", strconv.Itoa(round), target.Name, path.name,
							strconv.Itoa(sample), strconv.FormatInt(elapsed.Nanoseconds(), 10)}))
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				err := target.Call(ctx, fixture.Stop)
				cancel()
				fixture.Check(err)
				if fixture.HasChild(target.PID) {
					log.Fatalf("%s stop left a running Xray child", target.Name)
				}
			}
		}
		for _, writer := range writers {
			writer.Flush()
			fixture.Check(writer.Error())
		}
	}
}

func nodeStartPath(client instancesv1.XrayInstancesServiceClient, path startPath) func(context.Context) error {
	req := &instancesv1.XrayInstanceRequest{Id: "primary", ConfigJson: path.config, KeepAliveSeconds: new(uint32(60))}
	if path.seed {
		req.InitialUsers = []*instancesv1.XrayInitialInboundUsers{{InboundTag: "vless", Accounts: []*instancesv1.XrayInitialAccount{{
			Email: startEmail, Credential: &accountsv1.Credential{Protocol: "vless", Uuid: startUUID.String()},
		}}}}
	}
	return func(ctx context.Context) error {
		result, err := client.Start(ctx, req)
		if err == nil && (result.GetId() != "primary" || result.GetState() != instancesv1.XrayInstanceState_XRAY_INSTANCE_STATE_READY || result.GetStartAction() != path.action) {
			return fmt.Errorf("unexpected Start result %v", result)
		}
		return err
	}
}

func referenceStartPath(conn *grpc.ClientConn, method protoreflect.MethodDescriptor, path startPath) func(context.Context) error {
	req := dynamicpb.NewMessage(method.Input())
	fields := req.Descriptor().Fields()
	req.Set(fields.ByName("config"), protoreflect.ValueOfString(string(path.config)))
	req.Set(fields.ByName("keep_alive"), protoreflect.ValueOfUint64(60))
	users := req.Mutable(fields.ByName("users")).List()
	value := users.NewElement()
	user := value.Message()
	userFields := user.Descriptor().Fields()
	user.Set(userFields.ByName("email"), protoreflect.ValueOfString(startEmail))
	user.Mutable(userFields.ByName("inbounds")).List().Append(protoreflect.ValueOfString("vless"))
	proxies := user.Mutable(userFields.ByName("proxies")).Message()
	vless := proxies.Mutable(proxies.Descriptor().Fields().ByName("vless")).Message()
	vless.Set(vless.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString(startUUID.String()))
	users.Append(value)
	return func(ctx context.Context) error {
		result := dynamicpb.NewMessage(method.Output())
		if err := conn.Invoke(ctx, "/service.NodeService/Start", req, result); err != nil {
			return err
		}
		if !result.Get(result.Descriptor().Fields().ByName("started")).Bool() {
			return fmt.Errorf("Start reported an inactive Xray process")
		}
		return nil
	}
}

func startConfiguration(path startPath, origins [2]*httptest.Server) []byte {
	return fmt.Appendf(nil, `{
		"inbounds":[{"tag":"vless","listen":"127.0.0.1","port":%d,"protocol":"vless","settings":{"decryption":"none","clients":[]}}],
		"outbounds":[
			{"tag":"origin-0","protocol":"freedom","settings":{"redirect":%q,"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}},
			{"tag":"origin-1","protocol":"freedom","settings":{"redirect":%q,"finalRules":[{"action":"allow","ip":["127.0.0.1/32"]}]}}
		],
		"routing":{"rules":[{"type":"field","ruleTag":"origin","inboundTag":["vless"],"outboundTag":"origin-%d"}]}
	}`, path.port, origins[0].Listener.Addr().String(), origins[1].Listener.Addr().String(), path.origin)
}

func verifyStartTraffic(path startPath, targetPort int) error {
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", path.port), time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	request := append([]byte{0}, startUUID[:]...)
	request = append(request, 0, 1) // Empty addons and TCP command.
	request = binary.BigEndian.AppendUint16(request, uint16(targetPort))
	request = append(request, 1, 127, 0, 0, 1) // IPv4 loopback.
	request = append(request, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return fmt.Errorf("VLESS response: %w", err)
	}
	if header != [2]byte{} {
		return fmt.Errorf("unexpected VLESS response header %v", header)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || string(body) != strconv.Itoa(path.origin) {
		return fmt.Errorf("unexpected origin response %s %q", response.Status, body)
	}
	return nil
}
