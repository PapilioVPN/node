package services_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	configs "github.com/PapilioVPN/node/internal/apps/xray/runtime/configs/domain"
	instances "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/domain"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	"github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/configuration"
	handlers "github.com/xtls/xray-core/app/proxyman/command"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestSourceBatchCompilesOnceAndInvokesNativeOncePerAttemptedAccount(t *testing.T) {
	for _, size := range []int{1, 50, 51, 1000} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			items, raw, compiled := sourceBatchInput(t, size)
			compiles, calls := 0, 0
			compiler := sourceCompiler(func(_ context.Context, id string, sources []configs.Source) (configs.Compilation, error) {
				compiles++
				if id != "a" || len(sources) != 2 || sources[0].Name != "base.json" || sources[1].Name != "tail.json" || string(sources[1].Content) != "tail" {
					t.Error("source order or content changed", id, sources)
				}
				return configs.Compilation{Content: compiled}, nil
			})
			c := api.New(sourceConnection{invoke: func(method string, input any) error {
				request, ok := input.(*handlers.AlterInboundRequest)
				if !ok || method != "/xray.app.proxyman.command.HandlerService/AlterInbound" {
					t.Error("unexpected native preflight or operation", method)
					return domain.ErrInvalidArgument
				}
				var operation handlers.AddUserOperation
				if err := proto.Unmarshal(request.Operation.Value, &operation); err != nil || operation.User == nil {
					t.Error("invalid native addition", err)
					return domain.ErrInvalidArgument
				}
				content, _ := proto.Marshal(operation.User)
				if compiles != 1 || request.Tag != items[calls].InboundTag || !bytes.Equal(content, raw[size-1-calls]) {
					t.Error("not prepared once in selection order", calls, request.Tag)
				}
				calls++
				return nil
			}})
			service, _ := newService([]services.Binding{{ID: "a", Client: c}}, &execution{generation: 1, state: instances.Running})
			sources, _ := services.NewSources(service, configuration.Preparer{Compiler: compiler})
			result, err := sources.AddMany(context.Background(), "a", items, []configs.Source{{Name: "base.json", Content: []byte("base")}, {Name: "tail.json", Content: []byte("tail")}}, 1)
			if err != nil || compiles != 1 || calls != size || len(result.Items) != size || result.Operation.Effect != domain.Completed {
				t.Fatal(result.Operation, err, compiles, calls)
			}
			for i, item := range result.Items {
				if item.Index != i || item.Err != nil || !item.Attempted || !item.Operation.Acknowledged || item.Operation.Email != items[i].Email || item.Operation.InboundTag != items[i].InboundTag {
					t.Fatal(i, item)
				}
			}
			t.Logf("accounts=%d compilations=%d native SDK calls=%d", size, compiles, calls)
		})
	}
}

func TestSourceBatchReportsSelectionAndNativePartialErrors(t *testing.T) {
	for _, mode := range []string{"missing", "mismatch", "unsupported", "native"} {
		t.Run(mode, func(t *testing.T) {
			items, _, compiled := sourceBatchInput(t, 3)
			want, calls := domain.ErrInvalidArgument, 0
			if mode == "missing" {
				items[1].Email = "absent"
			}
			if mode == "mismatch" || mode == "unsupported" {
				var c core.Config
				_ = proto.Unmarshal(compiled, &c)
				// Only the selected inbound fails; other source payloads are intact.
				items[1].InboundTag = "bad"
				proxyType, accountType := "xray.proxy.vless.inbound.Config", "xray.proxy.trojan.Account"
				if mode == "unsupported" {
					proxyType, want = "unsupported", domain.ErrUnsupported
				}
				user, _ := proto.Marshal(&protocol.User{Email: items[1].Email, Account: &serial.TypedMessage{Type: accountType}})
				c.Inbound = append(c.Inbound, &core.InboundHandlerConfig{Tag: "bad", ProxySettings: &serial.TypedMessage{Type: proxyType, Value: protowire.AppendBytes(protowire.AppendTag(nil, 1, protowire.BytesType), user)}})
				compiled, _ = proto.Marshal(&c)
			}
			service, _ := newService([]services.Binding{{ID: "a", Client: client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
				calls++
				if mode == "native" && calls == 2 {
					return domain.NativeResult{Effect: domain.Unknown, RequiresInstanceRestart: true}, domain.ErrUncertain
				}
				return domain.NativeResult{Effect: domain.Completed, Acknowledged: true}, nil
			}}}}, &execution{generation: 1, state: instances.Running})
			sources, _ := services.NewSources(service, configuration.Preparer{Compiler: sourceCompiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
				return configs.Compilation{Content: compiled}, nil
			})})
			result, err := sources.AddMany(context.Background(), "a", items, nil, 1)
			if mode == "native" {
				if !errors.Is(err, domain.ErrUncertain) || calls != 2 || !result.Items[1].Attempted || result.Operation.Effect != domain.Unknown || !result.Operation.RequiresInstanceRestart {
					t.Fatal(result, err, calls)
				}
			} else if !errors.Is(err, want) || !errors.Is(result.Items[1].Err, want) || calls != 1 || result.Items[1].Attempted || result.Items[1].Operation.Email != items[1].Email || result.Operation.Effect != domain.Partial {
				t.Fatal(result, err, calls)
			}
			if !result.Items[0].Operation.Acknowledged || result.Items[2].Attempted || !errors.Is(result.Items[2].Err, domain.ErrNotAttempted) {
				t.Fatal(result)
			}
		})
	}
}

func TestSourceBatchPreparationFailureCancellationAndReplacement(t *testing.T) {
	for _, mode := range []string{"compile", "cancel", "replace", "invalid_parallelism", "empty", "too_many"} {
		t.Run(mode, func(t *testing.T) {
			items, _, compiled := sourceBatchInput(t, 3)
			e := &execution{generation: 1, state: instances.Running}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			compiles, calls, parallelism := 0, 0, 1
			service, _ := newService([]services.Binding{{ID: "a", Client: client{add: func(context.Context, string, []byte) (domain.NativeResult, error) {
				calls++
				return domain.NativeResult{}, nil
			}}}}, e)
			sources, _ := services.NewSources(service, configuration.Preparer{Compiler: sourceCompiler(func(context.Context, string, []configs.Source) (configs.Compilation, error) {
				compiles++
				if _, err := service.Count(context.Background(), "a", "members"); !errors.Is(err, domain.ErrCapacity) {
					t.Error("preparation escaped the live reservation", err)
				}
				switch mode {
				case "compile":
					return configs.Compilation{}, configs.ErrRejected
				case "cancel":
					cancel()
				case "replace":
					e.replace()
				}
				return configs.Compilation{Content: compiled}, nil
			})})
			wantCompiles := 1
			switch mode {
			case "invalid_parallelism":
				parallelism, wantCompiles = 5, 0
			case "empty":
				items, wantCompiles = nil, 0
			case "too_many":
				items, wantCompiles = make([]domain.SourceSelection, domain.MaxBatchItems+1), 0
			}
			result, err := sources.AddMany(ctx, "a", items, nil, parallelism)
			if err == nil || calls != 0 || compiles != wantCompiles {
				t.Fatal(result, err, calls, compiles)
			}
			if mode == "replace" && !errors.Is(err, domain.ErrExecutionChanged) {
				t.Fatal(err)
			}
			status, _ := service.Status(context.Background(), "a")
			if status.RequestInFlight {
				t.Fatal("reservation leaked")
			}
		})
	}
}
