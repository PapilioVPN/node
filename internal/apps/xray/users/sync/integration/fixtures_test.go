//go:build integration && linux

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	inboundservices "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/app/services"
	inboundapi "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/api"
	inboundconfiguration "github.com/PapilioVPN/node/internal/apps/xray/proxies/inbounds/infra/configuration"
	instanceservices "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/app/services"
	instanceprocess "github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/process"
	"github.com/PapilioVPN/node/internal/apps/xray/runtime/instances/infra/rpc"
	accountservices "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/app/services"
	accountapi "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/api"
	accountinbounds "github.com/PapilioVPN/node/internal/apps/xray/users/accounts/infra/inbounds"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/ports"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/app/services"
	"github.com/PapilioVPN/node/internal/apps/xray/users/sync/domain"
	accountadapter "github.com/PapilioVPN/node/internal/apps/xray/users/sync/infra/accounts"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/vless"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// revisionOnly supplies controlled desired revision facts for live-path tests.
// Full/recovery integration uses the actual Configs provider separately.
type revisionOnly struct{}

func (revisionOnly) Revision(context.Context, string) (uint64, error) { return 7, nil }
func (revisionOnly) PlanMembership(context.Context, ports.MembershipV1) (ports.ReviewV1, error) {
	return ports.ReviewV1{}, domain.ErrUnsupported
}
func (revisionOnly) ApplyMembership(context.Context, string, string) (ports.ResultV1, error) {
	return ports.ResultV1{}, domain.ErrUnsupported
}
func (revisionOnly) MembershipStatus(context.Context, string, string) (ports.ResultV1, error) {
	return ports.ResultV1{}, domain.ErrUnsupported
}
func (revisionOnly) RecoverMembership(context.Context, string, string, uint64) (ports.ResultV1, error) {
	return ports.ResultV1{}, domain.ErrUnsupported
}

type calls struct {
	grpc.ClientConnInterface
	mu      sync.Mutex
	methods map[string]int
}

func (c *calls) Invoke(ctx context.Context, method string, args, reply any, opts ...grpc.CallOption) error {
	c.mu.Lock()
	c.methods[method[strings.LastIndex(method, "/")+1:]]++
	c.mu.Unlock()
	return c.ClientConnInterface.Invoke(ctx, method, args, reply, opts...)
}
func (c *calls) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.methods
	c.methods = make(map[string]int)
	return result
}

type fixture struct {
	service         *services.Service
	calls           *calls
	instances       *instanceservices.Service
	address, target string
	accounts        *accountservices.Service
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	binary := os.Getenv("XRAY_TEST_BINARY")
	if binary == "" {
		t.Fatal("XRAY_TEST_BINARY must select the verified executable")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "sync-ok") }))
	t.Cleanup(target.Close)
	apiListener, accountListener := listen(t), listen(t)
	apiAddress, accountAddress := apiListener.Addr().String(), accountListener.Addr().String()
	_, targetPort, _ := net.SplitHostPort(strings.TrimPrefix(target.URL, "http://"))
	baseline := map[string]any{
		"log":       map[string]any{"loglevel": "none"},
		"api":       map[string]any{"tag": "api", "listen": apiAddress, "services": []string{"StatsService", "HandlerService"}},
		"stats":     map[string]any{},
		"inbounds":  []any{map[string]any{"tag": "selected", "listen": "127.0.0.1", "port": accountListener.Addr().(*net.TCPAddr).Port, "protocol": "vless", "settings": map[string]any{"decryption": "none", "clients": []any{}}}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "settings": map[string]any{"finalRules": []any{map[string]any{"action": "allow", "network": "tcp", "ip": []string{"127.0.0.1/32"}, "port": targetPort}}}}},
	}
	source, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	connection, err := rpc.New(rpc.Config{Network: "tcp", Address: apiAddress})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	runtime, err := instanceprocess.New(instanceprocess.Config{ID: "one", Binary: binary, Configuration: path, StopTimeout: time.Second, API: connection})
	if err != nil {
		t.Fatal(err)
	}
	instances, err := instanceservices.New([]instanceservices.Binding{{ID: "one", Engine: "xray", Runtime: runtime}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := instances.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	_ = apiListener.Close()
	_ = accountListener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := instances.Start(ctx, "one"); err != nil {
		t.Fatal(err)
	}
	for {
		if ready, err := runtime.Check(ctx); err == nil && ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("isolated Xray never ready")
		case <-time.After(10 * time.Millisecond):
		}
	}
	counter := &calls{ClientConnInterface: connection, methods: make(map[string]int)}
	nativeInbounds := inboundapi.New(counter)
	inbounds, err := inboundservices.New([]inboundservices.Binding{{ID: "one", Client: nativeInbounds, Projection: nativeInbounds}}, instances)
	if err != nil {
		t.Fatal(err)
	}
	catalog := inboundservices.NewCatalog(inbounds, inboundconfiguration.Inspector{})
	selection := accountinbounds.Reader{Source: catalog}
	nativeAccounts := accountapi.New(counter)
	accounts, err := accountservices.New([]accountservices.Binding{{ID: "one", Client: nativeAccounts, Reader: nativeAccounts}}, instances, selection, accountapi.Inspector{})
	if err != nil {
		t.Fatal(err)
	}
	observer := accountadapter.Observer{Source: accounts, Catalog: selection, Configs: revisionOnly{}}
	service, err := services.New([]string{"one"}, observer, accounts, revisionOnly{}, accountadapter.Codec{})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{service: service, calls: counter, instances: instances, address: accountAddress, target: strings.TrimPrefix(target.URL, "http://"), accounts: accounts}
}
func nativeUser(i int) []byte {
	account, _ := proto.Marshal(&vless.Account{Id: fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)})
	raw, _ := proto.Marshal(&protocol.User{Email: fmt.Sprintf("member-%d", i), Account: &serial.TypedMessage{Type: "xray.proxy.vless.Account", Value: account}})
	return raw
}
