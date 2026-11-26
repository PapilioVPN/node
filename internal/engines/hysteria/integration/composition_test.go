package integration

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/PapilioVPN/node/internal/engines/hysteria"
	"github.com/go-chi/chi/v5"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

func TestHysteriaFxModuleMakesRoutesOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name, want := "disabled", http.StatusNotFound
		if enabled {
			name, want = "enabled", http.StatusOK
		}
		t.Run(name, func(t *testing.T) {
			router, server := chi.NewRouter(), grpc.NewServer()
			defer server.Stop()
			config := hysteria.Config{}
			if enabled {
				dir := t.TempDir()
				config.Instances = []hysteria.InstanceConfig{{ID: "edge", Executable: filepath.Join(dir, "binary"), ConfigFile: filepath.Join(dir, "source"), RuntimeDirectory: dir}}
			}
			app := fx.New(fx.NopLogger, fx.Supply(config, router, server), hysteria.Module)
			if err := app.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := app.Stop(t.Context()); err != nil {
					t.Error(err)
				}
			}()
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, instance, nil))
			if w.Code != want {
				t.Fatalf("HTTP status %d, want %d", w.Code, want)
			}
			for _, service := range []string{"Instances", "Configs", "Stats", "Logs"} {
				_, registered := server.GetServiceInfo()["papiliovpn.node.v1.Hysteria"+service+"Service"]
				if registered != enabled {
					t.Fatalf("service %s registration %t", service, registered)
				}
			}
		})
	}
}
