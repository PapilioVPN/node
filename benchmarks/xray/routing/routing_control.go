package routing

import (
	"context"
	"fmt"
	"net/http"

	"github.com/PapilioVPN/node/benchmarks/xray/internal/fixture"

	routingv1 "github.com/PapilioVPN/node/internal/engines/xray/routing/bridge/grpc/proto/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	probeOp    = "test"
	overrideOp = "override"
	clearOp    = "clear"
)

type routingControl struct {
	probe func(context.Context) (string, error)
	set   func(context.Context, string) error
}

func routingHTTPControl(client *http.Client, address string, reference bool) routingControl {
	if reference {
		probe, err := proto.Marshal(referenceProbe())
		fixture.Check(err)
		return routingControl{
			probe: func(ctx context.Context) (string, error) {
				var result routingv1.XrayRouteSelection
				err := routingHTTPRead(ctx, client, http.MethodPost, address+"/routing/test", probe, &result, false)
				return result.GetOutboundTag(), err
			},
			set: func(ctx context.Context, target string) error {
				request := overrideFixture(target)
				return routingHTTPRead(ctx, client, http.MethodPut, address+"/routing/balancer/override",
					request.ProtoReflect().GetUnknown(), &emptypb.Empty{}, false)
			},
		}
	}
	base := address + "/v1/xray/instances/primary/routing"
	probe := []byte(`{"network":"tcp","target_ip":"192.0.2.1","target_port":2002}`)
	payloads := map[string][]byte{
		fixtureOverride: []byte(`{"target":"direct"}`),
		"":              []byte(`{"target":""}`),
	}
	return routingControl{
		probe: func(ctx context.Context) (string, error) {
			var result routingv1.XrayRouteSelection
			err := routingHTTPRead(ctx, client, http.MethodPost, base+"/tests", probe, &result, true)
			return result.GetOutboundTag(), err
		},
		set: func(ctx context.Context, target string) error {
			return routingHTTPRead(ctx, client, http.MethodPut, base+"/balancer/override?tag="+fixtureBalancer,
				payloads[target], nil, true)
		},
	}
}

func routingGRPCControl(conn *grpc.ClientConn, reference bool) routingControl {
	if reference {
		const service = "/service.NodeService/"
		probe := referenceProbe()
		return routingControl{
			probe: func(ctx context.Context) (string, error) {
				var result routingv1.XrayRouteSelection
				err := conn.Invoke(routingKey(ctx), service+"TestRoute", probe, &result, grpc.StaticMethod())
				return result.GetOutboundTag(), err
			},
			set: func(ctx context.Context, target string) error {
				return conn.Invoke(routingKey(ctx), service+"OverrideBalancerTarget", overrideFixture(target), &emptypb.Empty{}, grpc.StaticMethod())
			},
		}
	}
	client := routingv1.NewXrayRoutingServiceClient(conn)
	probe := &routingv1.XrayTestRouteRequest{Id: "primary", Network: "tcp", TargetIp: "192.0.2.1", TargetPort: 2002}
	return routingControl{
		probe: func(ctx context.Context) (string, error) {
			result, err := client.TestRoute(routingKey(ctx), probe)
			return result.GetOutboundTag(), err
		},
		set: func(ctx context.Context, target string) error {
			_, err := client.OverrideBalancerTarget(routingKey(ctx), &routingv1.XrayOverrideBalancerTargetRequest{
				Id: "primary", Tag: fixtureBalancer, Target: &target,
			})
			return err
		},
	}
}

func routingKey(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-api-key", fixture.Key)
}

// PasarGuard's TestRouteRequest fields at 739eab9. The outbound selectors give
// both products the same native response projection.
func referenceProbe() *emptypb.Empty {
	encoded := protowire.AppendString(protowire.AppendTag(nil, 2, protowire.BytesType), "tcp")
	encoded = protowire.AppendString(protowire.AppendTag(encoded, 3, protowire.BytesType), "192.0.2.1")
	encoded = protowire.AppendVarint(protowire.AppendTag(encoded, 5, protowire.VarintType), 2002)
	encoded = protowire.AppendString(protowire.AppendTag(encoded, 9, protowire.BytesType), "outbound")
	message := &emptypb.Empty{}
	message.ProtoReflect().SetUnknown(encoded)
	return message
}

func routingControlRead(calls routingControl, operation string) func(context.Context) (uint32, error) {
	if operation == probeOp {
		return func(ctx context.Context) (uint32, error) {
			tag, err := calls.probe(ctx)
			if err != nil {
				return 0, fixture.CallError(err)
			}
			if tag != "direct" {
				return 0, fmt.Errorf("unexpected route target: %q", tag)
			}
			return 0, nil
		}
	}
	return func(ctx context.Context) (uint32, error) {
		return 0, fixture.CallError(calls.set(ctx, overrideDestination(operation)))
	}
}

func overrideDestination(operation string) string {
	if operation == clearOp {
		return ""
	}
	return fixtureOverride
}
