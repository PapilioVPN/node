package liveupdate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/PapilioVPN/node/internal/engines/xray/configs/runconfig"
	handlercmd "github.com/xtls/xray-core/app/proxyman/command"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common/serial"
	xray "github.com/xtls/xray-core/core"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const routerConfigType = "xray.app.router.Config"
const maxNativeRequestBytes = 4 << 20

type call struct {
	method  string
	request proto.Message
}

type Change struct{ calls []call }

// Prepare uses the selected native executable to compile all request payloads
// before the first mutation. Native decoding and asset access stay in the child.
func (p Plan) Prepare(ctx context.Context, executable, runtimeDirectory string, source []byte) (Change, error) {
	if len(p.steps) == 0 {
		return Change{}, nil
	}
	compiled, err := compile(ctx, executable, runtimeDirectory, source)
	if err != nil {
		return Change{}, err
	}
	var routing *serial.TypedMessage
	for _, app := range compiled.GetApp() {
		if app.GetType() == routerConfigType {
			routing = app
			break
		}
	}
	outbounds := make(map[string]*xray.OutboundHandlerConfig, len(compiled.GetOutbound()))
	for _, outbound := range compiled.GetOutbound() {
		outbounds[outbound.GetTag()] = outbound
	}

	result := Change{calls: make([]call, 0, len(p.steps))}
	for _, step := range p.steps {
		var next call
		switch step.kind {
		case addOutbound:
			outbound := outbounds[step.tag]
			if outbound == nil {
				return Change{}, errors.New("compiled configuration is missing a requested outbound")
			}
			next = call{handlercmd.HandlerService_AddOutbound_FullMethodName, &handlercmd.AddOutboundRequest{Outbound: outbound}}
		case removeOutbound:
			next = call{handlercmd.HandlerService_RemoveOutbound_FullMethodName, &handlercmd.RemoveOutboundRequest{Tag: step.tag}}
		case replaceRouting:
			if routing == nil {
				return Change{}, errors.New("compiled configuration is missing the routing component")
			}
			next = call{routercmd.RoutingService_AddRule_FullMethodName, &routercmd.AddRuleRequest{Config: routing}}
		}
		if proto.Size(next.request) > maxNativeRequestBytes {
			return Change{}, errors.New("xray live change exceeds the native request limit")
		}
		result.calls = append(result.calls, next)
	}
	return result, nil
}

// Apply reports whether an RPC could have affected the running execution.
func (change Change) Apply(ctx context.Context, conn grpc.ClientConnInterface) (attempted bool, err error) {
	for _, operation := range change.calls {
		if err := ctx.Err(); err != nil {
			return attempted, err
		}
		attempted = true
		if err := conn.Invoke(ctx, operation.method, operation.request, &emptypb.Empty{},
			grpc.StaticMethod(), grpc.MaxCallRecvMsgSize(1024), grpc.MaxRetryRPCBufferSize(0)); err != nil {
			return attempted, fmt.Errorf("apply xray configuration with %s: %w", operation.method, err)
		}
	}
	return attempted, nil
}

func compile(ctx context.Context, executable, runtimeDirectory string, source []byte) (compiled *xray.Config, err error) {
	dir, err := os.MkdirTemp(runtimeDirectory, "node-xray-compile-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	input, output := filepath.Join(dir, "input.json"), filepath.Join(dir, "output.pb")
	if err := os.WriteFile(input, source, 0600); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, executable, "convert", "pb", "-o", output, input)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	runtime.LockOSThread()
	runErr := cmd.Run()
	runtime.UnlockOSThread()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, runErr
	}
	data, err := runconfig.ReadSource(output, 32<<20)
	if err != nil {
		return nil, err
	}
	compiled = new(xray.Config)
	if err := proto.Unmarshal(data, compiled); err != nil {
		return nil, err
	}
	return compiled, nil
}
